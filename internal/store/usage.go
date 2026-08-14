package store

import (
	"context"
	"fmt"
	"time"
)

// UsageEntry is one row of the local usage ledger (REQUIREMENTS.md §6.1
// item 2): exact tokens/cost the runner reported for one job's dispatch,
// broken down by model when the CLI reports per-model usage. Model is
// empty when it doesn't (e.g. a run whose result carried only aggregate
// usage) — such an entry still counts toward window totals, just not
// toward a per-model breakdown.
type UsageEntry struct {
	ID                       string
	JobID                    string
	Model                    string
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	OutputTokens             int
	CostUSD                  float64
	RecordedAt               time.Time
	// SourceUUID identifies the transcript line a backfilled entry came
	// from (FR-USAGE-2), enabling idempotent re-indexing. Empty for
	// entries from a live dispatch.
	SourceUUID string
}

// RecordUsage persists a usage ledger entry. It assigns an ID if one isn't
// set and stamps RecordedAt with the current time if it's zero.
func (s *Store) RecordUsage(ctx context.Context, e UsageEntry) (UsageEntry, error) {
	if e.ID == "" {
		id, err := newUsageEntryID()
		if err != nil {
			return UsageEntry{}, err
		}
		e.ID = id
	}
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_entries (
			id, job_id, model, input_tokens, cache_creation_input_tokens,
			cache_read_input_tokens, output_tokens, cost_usd, recorded_at, source_uuid
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		e.ID, e.JobID, e.Model, e.InputTokens, e.CacheCreationInputTokens,
		e.CacheReadInputTokens, e.OutputTokens, e.CostUSD, e.RecordedAt.Unix(), e.SourceUUID,
	)
	if err != nil {
		return UsageEntry{}, fmt.Errorf("inserting usage entry: %w", err)
	}
	return e, nil
}

// RecordUsageIfNew persists e, but silently does nothing (inserted=false,
// err=nil) if an entry with the same non-empty SourceUUID already exists.
// e.SourceUUID must be non-empty — this method exists specifically for
// FR-USAGE-2's idempotent backfill path; RecordUsage (no dedup) remains
// the entry point for live dispatches.
func (s *Store) RecordUsageIfNew(ctx context.Context, e UsageEntry) (inserted bool, err error) {
	if e.SourceUUID == "" {
		return false, fmt.Errorf("RecordUsageIfNew: SourceUUID is required")
	}
	if e.ID == "" {
		id, err := newUsageEntryID()
		if err != nil {
			return false, err
		}
		e.ID = id
	}
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO usage_entries (
			id, job_id, model, input_tokens, cache_creation_input_tokens,
			cache_read_input_tokens, output_tokens, cost_usd, recorded_at, source_uuid
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		e.ID, e.JobID, e.Model, e.InputTokens, e.CacheCreationInputTokens,
		e.CacheReadInputTokens, e.OutputTokens, e.CostUSD, e.RecordedAt.Unix(), e.SourceUUID,
	)
	if err != nil {
		return false, fmt.Errorf("inserting usage entry (idempotent): %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking rows affected: %w", err)
	}
	return n > 0, nil
}

// JobUsageTotal is one completed job's summed usage — the per-job
// granularity internal/budget's cost predictor needs (REQUIREMENTS.md
// §6.4: "a per-job cost predictor to decide a job is oversized").
type JobUsageTotal struct {
	JobID   string
	Tokens  int
	CostUSD float64
}

// CompletedJobUsageTotals returns one JobUsageTotal per Succeeded or Failed
// job of the given kind that has at least one usage_entries row, summing
// across models when a job's usage was recorded per-model (RecordResult).
// Running/Queued/etc. jobs are excluded — only a finished job's usage is a
// real observation of what that kind of work costs.
func (s *Store) CompletedJobUsageTotals(ctx context.Context, kind JobKind) ([]JobUsageTotal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.job_id, SUM(u.input_tokens + u.output_tokens), SUM(u.cost_usd)
		FROM usage_entries u
		JOIN jobs j ON j.id = u.job_id
		WHERE j.kind = ? AND j.status IN (?, ?)
		GROUP BY u.job_id
	`, string(kind), string(StatusSucceeded), string(StatusFailed))
	if err != nil {
		return nil, fmt.Errorf("summing completed job usage for kind %s: %w", kind, err)
	}
	defer rows.Close()

	var totals []JobUsageTotal
	for rows.Next() {
		var t JobUsageTotal
		if err := rows.Scan(&t.JobID, &t.Tokens, &t.CostUSD); err != nil {
			return nil, fmt.Errorf("scanning job usage total: %w", err)
		}
		totals = append(totals, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating job usage totals: %w", err)
	}
	return totals, nil
}

// ListUsageSince returns every usage entry recorded at or after since,
// ordered oldest first.
func (s *Store) ListUsageSince(ctx context.Context, since time.Time) ([]UsageEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, model, input_tokens, cache_creation_input_tokens,
			cache_read_input_tokens, output_tokens, cost_usd, recorded_at, source_uuid
		FROM usage_entries
		WHERE recorded_at >= ?
		ORDER BY recorded_at ASC
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("listing usage entries: %w", err)
	}
	defer rows.Close()

	var entries []UsageEntry
	for rows.Next() {
		var e UsageEntry
		var recordedAtUnix int64
		if err := rows.Scan(
			&e.ID, &e.JobID, &e.Model, &e.InputTokens, &e.CacheCreationInputTokens,
			&e.CacheReadInputTokens, &e.OutputTokens, &e.CostUSD, &recordedAtUnix, &e.SourceUUID,
		); err != nil {
			return nil, fmt.Errorf("scanning usage entry: %w", err)
		}
		e.RecordedAt = time.Unix(recordedAtUnix, 0).UTC()
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating usage entries: %w", err)
	}
	return entries, nil
}
