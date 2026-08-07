package budget

import (
	"context"
	"fmt"
	"time"

	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

// Ledger records dispatch outcomes and answers rolling-window usage
// queries on top of internal/store. It depends on internal/runner only for
// the Result type it consumes — it holds no dispatch or scheduling logic
// of its own.
type Ledger struct {
	store *store.Store
}

// New wraps a Ledger around s.
func New(s *store.Store) *Ledger {
	return &Ledger{store: s}
}

// HistoricalUsage is one already-occurred usage observation recovered by
// indexing ~/.claude/projects/**/*.jsonl (FR-USAGE-2), as opposed to a
// live dispatch's runner.Result. There is no dollar cost available in the
// transcript format this is parsed from, so CostUSD is always 0 for these
// entries — a deliberate limitation, not a bug.
type HistoricalUsage struct {
	JobID                    string // "interactive:<sessionID>" for non-tokenwarden sessions
	Model                    string
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	RecordedAt               time.Time
	SourceUUID               string // the transcript line's uuid; required, used for idempotent re-indexing
}

// RecordResult persists one dispatch's usage. When result reports
// per-model usage (the common case — see SPIKE-001), one ledger entry is
// written per model; otherwise a single entry is written from the
// aggregate Usage/TotalCostUSD fields, so a run is never silently dropped
// just because the CLI didn't break its cost down by model.
func (l *Ledger) RecordResult(ctx context.Context, jobID string, result runner.Result) error {
	if len(result.ModelUsage) == 0 {
		_, err := l.store.RecordUsage(ctx, store.UsageEntry{
			JobID:                    jobID,
			InputTokens:              result.Usage.InputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			OutputTokens:             result.Usage.OutputTokens,
			CostUSD:                  result.TotalCostUSD,
		})
		if err != nil {
			return fmt.Errorf("recording usage for job %s: %w", jobID, err)
		}
		return nil
	}

	for model, mu := range result.ModelUsage {
		_, err := l.store.RecordUsage(ctx, store.UsageEntry{
			JobID:                    jobID,
			Model:                    model,
			InputTokens:              mu.InputTokens,
			CacheCreationInputTokens: mu.CacheCreationInputTokens,
			CacheReadInputTokens:     mu.CacheReadInputTokens,
			OutputTokens:             mu.OutputTokens,
			CostUSD:                  mu.CostUSD,
		})
		if err != nil {
			return fmt.Errorf("recording usage for job %s (model %s): %w", jobID, model, err)
		}
	}
	return nil
}

// RecordHistoricalUsage persists h, skipping silently (inserted=false) if
// this exact transcript line (by SourceUUID) was already indexed.
func (l *Ledger) RecordHistoricalUsage(ctx context.Context, h HistoricalUsage) (inserted bool, err error) {
	inserted, err = l.store.RecordUsageIfNew(ctx, store.UsageEntry{
		JobID:                    h.JobID,
		Model:                    h.Model,
		InputTokens:              h.InputTokens,
		OutputTokens:             h.OutputTokens,
		CacheCreationInputTokens: h.CacheCreationInputTokens,
		CacheReadInputTokens:     h.CacheReadInputTokens,
		RecordedAt:               h.RecordedAt,
		SourceUUID:               h.SourceUUID,
	})
	if err != nil {
		return false, fmt.Errorf("recording historical usage (source %s): %w", h.SourceUUID, err)
	}
	return inserted, nil
}
