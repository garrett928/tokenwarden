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

func TestListGroundTruth_OrdersOldestFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Record in non-chronological order
	oldest := GroundTruthReading{
		FiveHourUsedPercentage: 10,
		FiveHourResetsAt:       now.Add(time.Hour),
		SevenDayUsedPercentage: 5,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now.Add(-2 * time.Hour),
	}
	newest := GroundTruthReading{
		FiveHourUsedPercentage: 30,
		FiveHourResetsAt:       now.Add(time.Hour),
		SevenDayUsedPercentage: 15,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now,
	}
	middle := GroundTruthReading{
		FiveHourUsedPercentage: 20,
		FiveHourResetsAt:       now.Add(time.Hour),
		SevenDayUsedPercentage: 10,
		SevenDayResetsAt:       now.Add(24 * time.Hour),
		ObservedAt:             now.Add(-1 * time.Hour),
	}

	// Insert in reverse order (newest, middle, oldest)
	if _, err := s.RecordGroundTruth(ctx, newest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGroundTruth(ctx, middle); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGroundTruth(ctx, oldest); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListGroundTruth(ctx)
	if err != nil {
		t.Fatalf("ListGroundTruth() error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListGroundTruth() returned %d readings, want 3", len(got))
	}

	// Verify order is oldest to newest
	if got[0].FiveHourUsedPercentage != 10 {
		t.Errorf("First reading (oldest) FiveHourUsedPercentage = %d, want 10", got[0].FiveHourUsedPercentage)
	}
	if got[1].FiveHourUsedPercentage != 20 {
		t.Errorf("Second reading (middle) FiveHourUsedPercentage = %d, want 20", got[1].FiveHourUsedPercentage)
	}
	if got[2].FiveHourUsedPercentage != 30 {
		t.Errorf("Third reading (newest) FiveHourUsedPercentage = %d, want 30", got[2].FiveHourUsedPercentage)
	}
}

func TestListGroundTruth_EmptyWhenNothingRecorded(t *testing.T) {
	s := openTestStore(t)
	got, err := s.ListGroundTruth(context.Background())
	if err != nil {
		t.Fatalf("ListGroundTruth() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListGroundTruth() on an empty store returned %d readings, want 0", len(got))
	}
}
