package budget

import (
	"context"
	"testing"
	"time"

	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

func openTestLedger(t *testing.T) *Ledger {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func TestRecordResult_PerModelUsage(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()

	result := runner.Result{
		TotalCostUSD: 0.03,
		ModelUsage: map[string]runner.ModelUsage{
			"claude-haiku-4-5-20251001": {InputTokens: 10, OutputTokens: 5, CostUSD: 0.02},
			"claude-sonnet-4-5":         {InputTokens: 20, OutputTokens: 8, CostUSD: 0.01},
		},
	}

	if err := l.RecordResult(ctx, "job_1", result); err != nil {
		t.Fatalf("RecordResult() error: %v", err)
	}

	totals, err := l.FiveHourTotal(ctx, time.Now())
	if err != nil {
		t.Fatalf("FiveHourTotal() error: %v", err)
	}
	if totals.EntryCount != 2 {
		t.Errorf("EntryCount = %d, want 2 (one row per model)", totals.EntryCount)
	}
	if totals.CostUSD != 0.03 {
		t.Errorf("CostUSD = %v, want 0.03 (sum of per-model costs)", totals.CostUSD)
	}
	if totals.InputTokens != 30 {
		t.Errorf("InputTokens = %d, want 30", totals.InputTokens)
	}
}

func TestRecordResult_AggregateFallbackWhenNoModelUsage(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()

	result := runner.Result{
		TotalCostUSD: 0.05,
		Usage: runner.Usage{
			InputTokens:  100,
			OutputTokens: 20,
		},
	}

	if err := l.RecordResult(ctx, "job_2", result); err != nil {
		t.Fatalf("RecordResult() error: %v", err)
	}

	totals, err := l.FiveHourTotal(ctx, time.Now())
	if err != nil {
		t.Fatalf("FiveHourTotal() error: %v", err)
	}
	if totals.EntryCount != 1 {
		t.Errorf("EntryCount = %d, want 1 (aggregate fallback, single row)", totals.EntryCount)
	}
	if totals.CostUSD != 0.05 || totals.InputTokens != 100 || totals.OutputTokens != 20 {
		t.Errorf("totals = %+v, want the aggregate Usage/TotalCostUSD fields", totals)
	}
}

func TestFiveHourTotal_ExcludesOlderEntries(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if _, err := l.store.RecordUsage(ctx, store.UsageEntry{JobID: "old", RecordedAt: now.Add(-6 * time.Hour), CostUSD: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.store.RecordUsage(ctx, store.UsageEntry{JobID: "recent", RecordedAt: now.Add(-1 * time.Hour), CostUSD: 2}); err != nil {
		t.Fatal(err)
	}

	totals, err := l.FiveHourTotal(ctx, now)
	if err != nil {
		t.Fatalf("FiveHourTotal() error: %v", err)
	}
	if totals.EntryCount != 1 || totals.CostUSD != 2 {
		t.Errorf("FiveHourTotal() = %+v, want only the entry from 1h ago (cost 2)", totals)
	}
}

func TestSevenDayTotal_ExcludesOlderEntries(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if _, err := l.store.RecordUsage(ctx, store.UsageEntry{JobID: "old", RecordedAt: now.Add(-8 * 24 * time.Hour), CostUSD: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.store.RecordUsage(ctx, store.UsageEntry{JobID: "within-week", RecordedAt: now.Add(-3 * 24 * time.Hour), CostUSD: 2}); err != nil {
		t.Fatal(err)
	}

	totals, err := l.SevenDayTotal(ctx, now)
	if err != nil {
		t.Fatalf("SevenDayTotal() error: %v", err)
	}
	if totals.EntryCount != 1 || totals.CostUSD != 2 {
		t.Errorf("SevenDayTotal() = %+v, want only the entry from 3 days ago (cost 2)", totals)
	}
}

func TestWindowTotal_EmptyLedger(t *testing.T) {
	l := openTestLedger(t)
	totals, err := l.FiveHourTotal(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("FiveHourTotal() error: %v", err)
	}
	if totals.EntryCount != 0 || totals.CostUSD != 0 {
		t.Errorf("FiveHourTotal() on empty ledger = %+v, want zero value", totals)
	}
}

func TestRecordHistoricalUsage_Idempotent(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	h := HistoricalUsage{JobID: "interactive:sess-1", Model: "claude-sonnet-5", InputTokens: 10, OutputTokens: 5, SourceUUID: "msg-1"}

	inserted1, err := l.RecordHistoricalUsage(ctx, h)
	if err != nil || !inserted1 {
		t.Fatalf("first RecordHistoricalUsage() = %v, %v; want true, nil", inserted1, err)
	}
	inserted2, err := l.RecordHistoricalUsage(ctx, h)
	if err != nil || inserted2 {
		t.Fatalf("second RecordHistoricalUsage() = %v, %v; want false, nil (idempotent)", inserted2, err)
	}

	totals, err := l.FiveHourTotal(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if totals.EntryCount != 1 {
		t.Errorf("EntryCount = %d, want 1 (no duplicate)", totals.EntryCount)
	}
	if totals.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", totals.InputTokens)
	}
}

func TestUsageForJob_SumsOnlyThatJobsEntries(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()

	if err := l.RecordResult(ctx, "job_a", runner.Result{
		TotalCostUSD: 0.05,
		Usage:        runner.Usage{InputTokens: 10, OutputTokens: 5},
	}); err != nil {
		t.Fatalf("RecordResult(job_a) error: %v", err)
	}
	if err := l.RecordResult(ctx, "job_b", runner.Result{
		TotalCostUSD: 0.10,
		Usage:        runner.Usage{InputTokens: 20, OutputTokens: 8},
	}); err != nil {
		t.Fatalf("RecordResult(job_b) error: %v", err)
	}

	totals, err := l.UsageForJob(ctx, "job_a")
	if err != nil {
		t.Fatalf("UsageForJob(job_a) error: %v", err)
	}
	if totals.EntryCount != 1 {
		t.Errorf("EntryCount = %d, want 1 (only job_a's entry)", totals.EntryCount)
	}
	if totals.CostUSD != 0.05 {
		t.Errorf("CostUSD = %v, want 0.05 (job_a's cost, not job_b's)", totals.CostUSD)
	}
	if totals.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", totals.InputTokens)
	}
}

func TestUsageForJob_NeverDispatchedIsZeroNotError(t *testing.T) {
	l := openTestLedger(t)

	totals, err := l.UsageForJob(context.Background(), "job_never_ran")
	if err != nil {
		t.Fatalf("UsageForJob() error: %v", err)
	}
	if totals.EntryCount != 0 {
		t.Errorf("EntryCount = %d, want 0", totals.EntryCount)
	}
}
