package budget

import (
	"context"
	"math"
	"testing"
	"time"

	"tokenwarden/internal/store"
)

func TestCalibrateFiveHour_InsufficientWithFewerThanThreeSamples(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	windowReset := now.Add(time.Hour)

	// Record 2 ground-truth readings with same window reset time, increasing percentage
	reading1 := GroundTruthReading{
		FiveHourUsedPercentage: 10,
		FiveHourResetsAt:       windowReset,
		SevenDayUsedPercentage: 5,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now,
	}
	reading2 := GroundTruthReading{
		FiveHourUsedPercentage: 12,
		FiveHourResetsAt:       windowReset,
		SevenDayUsedPercentage: 7,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now.Add(1 * time.Hour),
	}

	if err := l.RecordGroundTruth(ctx, reading1); err != nil {
		t.Fatalf("RecordGroundTruth() error: %v", err)
	}
	if err := l.RecordGroundTruth(ctx, reading2); err != nil {
		t.Fatalf("RecordGroundTruth() error: %v", err)
	}

	// Record a usage entry between the two readings
	_, err := l.store.RecordUsage(ctx, store.UsageEntry{
		JobID:        "job_1",
		Model:        "haiku",
		InputTokens:  100,
		OutputTokens: 50,
		RecordedAt:   now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordUsage() error: %v", err)
	}

	got, err := l.CalibrateFiveHour(ctx)
	if err != nil {
		t.Fatalf("CalibrateFiveHour() error: %v", err)
	}
	if !got.Insufficient {
		t.Errorf("CalibrateFiveHour() Insufficient = false, want true with only 1 sample pair")
	}
	if got.Samples != 1 {
		t.Errorf("CalibrateFiveHour() Samples = %d, want 1", got.Samples)
	}
}

func TestCalibrateFiveHour_FitsFromMultiplePairs(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	windowReset := now.Add(time.Hour)

	// Record 4 ground-truth readings all sharing the same FiveHourResetsAt
	// with FiveHourUsedPercentage increasing by 1 each time (10, 11, 12, 13)
	readings := []GroundTruthReading{
		{
			FiveHourUsedPercentage: 10,
			FiveHourResetsAt:       windowReset,
			SevenDayUsedPercentage: 5,
			SevenDayResetsAt:       now.Add(24 * time.Hour),
			ObservedAt:             now,
		},
		{
			FiveHourUsedPercentage: 11,
			FiveHourResetsAt:       windowReset,
			SevenDayUsedPercentage: 6,
			SevenDayResetsAt:       now.Add(24 * time.Hour),
			ObservedAt:             now.Add(1 * time.Hour),
		},
		{
			FiveHourUsedPercentage: 12,
			FiveHourResetsAt:       windowReset,
			SevenDayUsedPercentage: 7,
			SevenDayResetsAt:       now.Add(24 * time.Hour),
			ObservedAt:             now.Add(2 * time.Hour),
		},
		{
			FiveHourUsedPercentage: 13,
			FiveHourResetsAt:       windowReset,
			SevenDayUsedPercentage: 8,
			SevenDayResetsAt:       now.Add(24 * time.Hour),
			ObservedAt:             now.Add(3 * time.Hour),
		},
	}

	for i, r := range readings {
		if err := l.RecordGroundTruth(ctx, r); err != nil {
			t.Fatalf("RecordGroundTruth() error: %v", err)
		}

		// Between each consecutive pair, record a usage entry with 1000 tokens
		if i > 0 {
			_, err := l.store.RecordUsage(ctx, store.UsageEntry{
				JobID:        "job_" + string(rune(i)),
				Model:        "haiku",
				InputTokens:  1000,
				OutputTokens: 0,
				RecordedAt:   readings[i-1].ObservedAt.Add(30 * time.Minute),
			})
			if err != nil {
				t.Fatalf("RecordUsage() error: %v", err)
			}
		}
	}

	got, err := l.CalibrateFiveHour(ctx)
	if err != nil {
		t.Fatalf("CalibrateFiveHour() error: %v", err)
	}
	if got.Insufficient {
		t.Errorf("CalibrateFiveHour() Insufficient = true, want false with 3 sample pairs")
	}
	if got.Samples != 3 {
		t.Errorf("CalibrateFiveHour() Samples = %d, want 3", got.Samples)
	}
	if math.Abs(got.TokensPerPercent-1000) > 0.01 {
		t.Errorf("CalibrateFiveHour() TokensPerPercent = %.2f, want ~1000", got.TokensPerPercent)
	}
}

func TestCalibrateFiveHour_IgnoresPairsAcrossWindowRollover(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Record 2 readings with different FiveHourResetsAt (simulating a window reset between them)
	reading1 := GroundTruthReading{
		FiveHourUsedPercentage: 50,
		FiveHourResetsAt:       now.Add(time.Hour),
		SevenDayUsedPercentage: 10,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now,
	}
	reading2 := GroundTruthReading{
		FiveHourUsedPercentage: 60,
		FiveHourResetsAt:       now.Add(2 * time.Hour), // Different reset time
		SevenDayUsedPercentage: 15,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now.Add(1 * time.Hour),
	}

	if err := l.RecordGroundTruth(ctx, reading1); err != nil {
		t.Fatalf("RecordGroundTruth() error: %v", err)
	}
	if err := l.RecordGroundTruth(ctx, reading2); err != nil {
		t.Fatalf("RecordGroundTruth() error: %v", err)
	}

	// Record a usage entry between the two readings
	_, err := l.store.RecordUsage(ctx, store.UsageEntry{
		JobID:        "job_1",
		Model:        "haiku",
		InputTokens:  1000,
		OutputTokens: 0,
		RecordedAt:   now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordUsage() error: %v", err)
	}

	got, err := l.CalibrateFiveHour(ctx)
	if err != nil {
		t.Fatalf("CalibrateFiveHour() error: %v", err)
	}
	if !got.Insufficient {
		t.Errorf("CalibrateFiveHour() Insufficient = false, want true (pair should be skipped due to window rollover)")
	}
	if got.Samples != 0 {
		t.Errorf("CalibrateFiveHour() Samples = %d, want 0 (pair across rollover should not count)", got.Samples)
	}
}

func TestCalibrateSevenDay_FitsFromMultiplePairs(t *testing.T) {
	l := openTestLedger(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	windowReset := now.Add(48 * time.Hour)

	// Record 4 ground-truth readings all sharing the same SevenDayResetsAt
	// with SevenDayUsedPercentage increasing by 1 each time (10, 11, 12, 13)
	readings := []GroundTruthReading{
		{
			FiveHourUsedPercentage: 5,
			FiveHourResetsAt:       now.Add(time.Hour),
			SevenDayUsedPercentage: 10,
			SevenDayResetsAt:       windowReset,
			ObservedAt:             now,
		},
		{
			FiveHourUsedPercentage: 6,
			FiveHourResetsAt:       now.Add(time.Hour),
			SevenDayUsedPercentage: 11,
			SevenDayResetsAt:       windowReset,
			ObservedAt:             now.Add(1 * time.Hour),
		},
		{
			FiveHourUsedPercentage: 7,
			FiveHourResetsAt:       now.Add(time.Hour),
			SevenDayUsedPercentage: 12,
			SevenDayResetsAt:       windowReset,
			ObservedAt:             now.Add(2 * time.Hour),
		},
		{
			FiveHourUsedPercentage: 8,
			FiveHourResetsAt:       now.Add(time.Hour),
			SevenDayUsedPercentage: 13,
			SevenDayResetsAt:       windowReset,
			ObservedAt:             now.Add(3 * time.Hour),
		},
	}

	for i, r := range readings {
		if err := l.RecordGroundTruth(ctx, r); err != nil {
			t.Fatalf("RecordGroundTruth() error: %v", err)
		}

		// Between each consecutive pair, record a usage entry with 1000 tokens
		if i > 0 {
			_, err := l.store.RecordUsage(ctx, store.UsageEntry{
				JobID:        "job_" + string(rune(i)),
				Model:        "haiku",
				InputTokens:  1000,
				OutputTokens: 0,
				RecordedAt:   readings[i-1].ObservedAt.Add(30 * time.Minute),
			})
			if err != nil {
				t.Fatalf("RecordUsage() error: %v", err)
			}
		}
	}

	got, err := l.CalibrateSevenDay(ctx)
	if err != nil {
		t.Fatalf("CalibrateSevenDay() error: %v", err)
	}
	if got.Insufficient {
		t.Errorf("CalibrateSevenDay() Insufficient = true, want false with 3 sample pairs")
	}
	if got.Samples != 3 {
		t.Errorf("CalibrateSevenDay() Samples = %d, want 3", got.Samples)
	}
	if math.Abs(got.TokensPerPercent-1000) > 0.01 {
		t.Errorf("CalibrateSevenDay() TokensPerPercent = %.2f, want ~1000", got.TokensPerPercent)
	}
}
