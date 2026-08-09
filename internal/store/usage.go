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

// ListUsageByJobID returns every usage entry recorded for jobID, ordered
// oldest first. A job with no entries yet (not dispatched, or dispatched
// but no result recorded) returns an empty slice, not an error.
func (s *Store) ListUsageByJobID(ctx context.Context, jobID string) ([]UsageEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, model, input_tokens, cache_creation_input_tokens,
			cache_read_input_tokens, output_tokens, cost_usd, recorded_at, source_uuid
		FROM usage_entries
		WHERE job_id = ?
		ORDER BY recorded_at ASC
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("listing usage entries for job %s: %w", jobID, err)
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
		return nil, fmt.Errorf("iterating usage entries for job %s: %w", jobID, err)
	}
	return entries, nil
}
