package budget

import (
	"context"
	"testing"
	"time"
)

func TestRecordGroundTruth_AndRetrieveLatest(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	reading := GroundTruthReading{
		FiveHourUsedPercentage: 89,
		FiveHourResetsAt:       now.Add(time.Hour),
		SevenDayUsedPercentage: 22,
		SevenDayResetsAt:       now.Add(48 * time.Hour),
		ObservedAt:             now,
	}
	if err := l.RecordGroundTruth(ctx, reading); err != nil {
		t.Fatalf("RecordGroundTruth() error: %v", err)
	}

	got, ok, err := l.LatestGroundTruth(ctx)
	if err != nil {
		t.Fatalf("LatestGroundTruth() error: %v", err)
	}
	if !ok {
		t.Fatal("LatestGroundTruth() ok = false, want true after recording a reading")
	}
	if got.FiveHourUsedPercentage != 89 || got.SevenDayUsedPercentage != 22 {
		t.Errorf("LatestGroundTruth() = %+v, want the recorded reading", got)
	}
	if !got.FiveHourResetsAt.Equal(reading.FiveHourResetsAt) {
		t.Errorf("FiveHourResetsAt = %v, want %v", got.FiveHourResetsAt, reading.FiveHourResetsAt)
	}
}

func TestLatestGroundTruth_NotOKWhenNoneRecorded(t *testing.T) {
	l := openTestLedger(t)
	got, ok, err := l.LatestGroundTruth(context.Background())
	if err != nil {
		t.Fatalf("LatestGroundTruth() error: %v", err)
	}
	if ok {
		t.Errorf("LatestGroundTruth() ok = true on an empty store, want false; got %+v", got)
	}
}

func TestRecordGroundTruth_LatestReturnsNewestOfMultiple(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if err := l.RecordGroundTruth(ctx, GroundTruthReading{
		FiveHourUsedPercentage: 10, ObservedAt: now.Add(-time.Hour),
		FiveHourResetsAt: now, SevenDayResetsAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordGroundTruth(ctx, GroundTruthReading{
		FiveHourUsedPercentage: 95, ObservedAt: now,
		FiveHourResetsAt: now, SevenDayResetsAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := l.LatestGroundTruth(ctx)
	if err != nil || !ok {
		t.Fatalf("LatestGroundTruth() = %+v, %v, %v", got, ok, err)
	}
	if got.FiveHourUsedPercentage != 95 {
		t.Errorf("FiveHourUsedPercentage = %d, want 95 (the newer reading)", got.FiveHourUsedPercentage)
	}
}
