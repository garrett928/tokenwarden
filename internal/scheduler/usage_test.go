package scheduler

import (
	"context"
	"testing"
	"time"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/store"
)

func newTestLedger(t *testing.T) *budget.Ledger {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return budget.New(s)
}

func TestComputeUsageState_NoDataAtAll(t *testing.T) {
	ledger := newTestLedger(t)
	now := time.Now()

	got, err := computeUsageState(context.Background(), ledger, now)
	if err != nil {
		t.Fatalf("computeUsageState() error: %v", err)
	}
	if got.Source != SourceUnknown {
		t.Errorf("Source = %v, want SourceUnknown", got.Source)
	}
	if got.FiveHourUsedPercent != 0 || got.SevenDayUsedPercent != 0 {
		t.Errorf("expected 0%% used with no data, got five_hour=%d seven_day=%d", got.FiveHourUsedPercent, got.SevenDayUsedPercent)
	}
}

func TestComputeUsageState_FreshGroundTruth_UsedDirectly(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()
	now := time.Now()

	reading := budget.GroundTruthReading{
		FiveHourUsedPercentage: 42,
		FiveHourResetsAt:       now.Add(2 * time.Hour),
		SevenDayUsedPercentage: 10,
		SevenDayResetsAt:       now.Add(3 * 24 * time.Hour),
		ObservedAt:             now.Add(-5 * time.Minute),
	}
	if err := ledger.RecordGroundTruth(ctx, reading); err != nil {
		t.Fatal(err)
	}

	got, err := computeUsageState(ctx, ledger, now)
	if err != nil {
		t.Fatalf("computeUsageState() error: %v", err)
	}
	if got.Source != SourceGroundTruth {
		t.Errorf("Source = %v, want SourceGroundTruth", got.Source)
	}
	if got.FiveHourUsedPercent != 42 || got.SevenDayUsedPercent != 10 {
		t.Errorf("got five_hour=%d seven_day=%d, want 42/10", got.FiveHourUsedPercent, got.SevenDayUsedPercent)
	}
}

func TestComputeUsageState_StaleGroundTruth_FallsBackButKeepsResetTimes(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()
	now := time.Now()

	staleResetsAt := now.Add(1 * time.Hour)
	reading := budget.GroundTruthReading{
		FiveHourUsedPercentage: 20,
		FiveHourResetsAt:       staleResetsAt,
		SevenDayUsedPercentage: 5,
		SevenDayResetsAt:       now.Add(2 * 24 * time.Hour),
		ObservedAt:             now.Add(-2 * time.Hour), // older than groundTruthFreshness
	}
	if err := ledger.RecordGroundTruth(ctx, reading); err != nil {
		t.Fatal(err)
	}

	got, err := computeUsageState(ctx, ledger, now)
	if err != nil {
		t.Fatalf("computeUsageState() error: %v", err)
	}
	if got.Source == SourceGroundTruth {
		t.Errorf("Source = %v, want fallback (stale reading shouldn't count as fresh)", got.Source)
	}
	if got.FiveHourResetsAt.Unix() != staleResetsAt.Unix() {
		t.Errorf("FiveHourResetsAt = %v, want stale reading's %v (better than nothing)", got.FiveHourResetsAt, staleResetsAt)
	}
}
