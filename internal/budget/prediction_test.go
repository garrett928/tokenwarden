package budget

import (
	"context"
	"math"
	"testing"

	"tokenwarden/internal/store"
)

// completedJob creates and terminates a job of kind with one usage entry
// (tokens, costUSD) attributed to it, so PredictJobCost has a real
// Succeeded job to join against — unlike calibration's tests, this query
// joins usage_entries to jobs, so an arbitrary unattached JobID string
// won't be picked up.
func completedJob(t *testing.T, l *Ledger, kind store.JobKind, tokens int, costUSD float64) {
	t.Helper()
	ctx := context.Background()

	j, err := l.store.CreateJob(ctx, store.Job{Kind: kind, Prompt: "do the thing"})
	if err != nil {
		t.Fatalf("CreateJob() error: %v", err)
	}
	if err := l.store.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}
	if _, err := l.store.RecordUsage(ctx, store.UsageEntry{JobID: j.ID, InputTokens: tokens, CostUSD: costUSD}); err != nil {
		t.Fatalf("RecordUsage() error: %v", err)
	}
}

func TestPredictJobCost_InsufficientWithFewerThanThreeSamples(t *testing.T) {
	l := openTestLedger(t)
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)
	completedJob(t, l, store.JobKindResearch, 2000, 0.20)

	got, err := l.PredictJobCost(context.Background(), store.JobKindResearch)
	if err != nil {
		t.Fatalf("PredictJobCost() error: %v", err)
	}
	if !got.Insufficient {
		t.Errorf("Insufficient = false, want true with only 2 samples")
	}
	if got.Samples != 2 {
		t.Errorf("Samples = %d, want 2", got.Samples)
	}
}

func TestPredictJobCost_MeanOfCompletedJobs(t *testing.T) {
	l := openTestLedger(t)
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)
	completedJob(t, l, store.JobKindResearch, 2000, 0.20)
	completedJob(t, l, store.JobKindResearch, 3000, 0.30)

	got, err := l.PredictJobCost(context.Background(), store.JobKindResearch)
	if err != nil {
		t.Fatalf("PredictJobCost() error: %v", err)
	}
	if got.Insufficient {
		t.Fatal("Insufficient = true, want false with 3 samples")
	}
	if got.Tokens != 2000 {
		t.Errorf("Tokens = %d, want 2000 (mean of 1000/2000/3000)", got.Tokens)
	}
	if math.Abs(got.CostUSD-0.20) > 1e-9 {
		t.Errorf("CostUSD = %v, want ~0.20 (mean of 0.10/0.20/0.30)", got.CostUSD)
	}
}

func TestPredictJobCost_ScopedByKind(t *testing.T) {
	l := openTestLedger(t)
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)
	completedJob(t, l, store.JobKindCode, 9000, 9.0)

	got, err := l.PredictJobCost(context.Background(), store.JobKindCode)
	if err != nil {
		t.Fatalf("PredictJobCost() error: %v", err)
	}
	if !got.Insufficient {
		t.Errorf("Insufficient = false, want true — only 1 completed job of kind code, research jobs shouldn't count")
	}
}

func TestPredictJobCost_ExcludesNonTerminalJobs(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)
	completedJob(t, l, store.JobKindResearch, 1000, 0.10)

	// A currently-Running job with usage recorded so far shouldn't count —
	// its total isn't final yet.
	running, err := l.store.CreateJob(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "still going"})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.store.UpdateStatus(ctx, running.ID, store.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.store.RecordUsage(ctx, store.UsageEntry{JobID: running.ID, InputTokens: 999999, CostUSD: 999}); err != nil {
		t.Fatal(err)
	}

	got, err := l.PredictJobCost(ctx, store.JobKindResearch)
	if err != nil {
		t.Fatalf("PredictJobCost() error: %v", err)
	}
	if got.Insufficient {
		t.Fatal("Insufficient = true, want false with 3 completed samples")
	}
	if got.Tokens != 1000 {
		t.Errorf("Tokens = %d, want 1000 (running job's usage must not be counted)", got.Tokens)
	}
}
