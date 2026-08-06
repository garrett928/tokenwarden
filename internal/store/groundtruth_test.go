package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecordGroundTruth_AssignsIDAndTimestamp(t *testing.T) {
	s := openTestStore(t)
	before := time.Now().Add(-time.Second)

	got, err := s.RecordGroundTruth(context.Background(), GroundTruthReading{
		FiveHourUsedPercentage: 89,
		FiveHourResetsAt:       time.Now().Add(time.Hour),
		SevenDayUsedPercentage: 22,
		SevenDayResetsAt:       time.Now().Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("RecordGroundTruth() error: %v", err)
	}
	if got.ID == "" {
		t.Error("RecordGroundTruth() did not assign an ID")
	}
	if got.ObservedAt.Before(before) {
		t.Errorf("ObservedAt = %v, want after %v", got.ObservedAt, before)
	}
}

func TestLatestGroundTruth_ReturnsMostRecent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	older := GroundTruthReading{
		FiveHourUsedPercentage: 50,
		FiveHourResetsAt:       now.Add(time.Hour),
		SevenDayUsedPercentage: 10,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now.Add(-time.Hour),
	}
	newer := GroundTruthReading{
		FiveHourUsedPercentage: 89,
		FiveHourResetsAt:       now.Add(2 * time.Hour),
		SevenDayUsedPercentage: 22,
		SevenDayResetsAt:       now.Add(48 * time.Hour),
		ObservedAt:             now,
	}

	if _, err := s.RecordGroundTruth(ctx, older); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGroundTruth(ctx, newer); err != nil {
		t.Fatal(err)
	}

	got, err := s.LatestGroundTruth(ctx)
	if err != nil {
		t.Fatalf("LatestGroundTruth() error: %v", err)
	}
	if got.FiveHourUsedPercentage != 89 || got.SevenDayUsedPercentage != 22 {
		t.Errorf("LatestGroundTruth() = %+v, want the newer reading (89/22)", got)
	}
	if !got.FiveHourResetsAt.Equal(newer.FiveHourResetsAt) {
		t.Errorf("FiveHourResetsAt = %v, want %v", got.FiveHourResetsAt, newer.FiveHourResetsAt)
	}
	if !got.SevenDayResetsAt.Equal(newer.SevenDayResetsAt) {
		t.Errorf("SevenDayResetsAt = %v, want %v", got.SevenDayResetsAt, newer.SevenDayResetsAt)
	}
}

func TestLatestGroundTruth_NotFoundWhenEmpty(t *testing.T) {
	s := openTestStore(t)
	_, err := s.LatestGroundTruth(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("LatestGroundTruth() error = %v, want ErrNotFound", err)
	}
}
