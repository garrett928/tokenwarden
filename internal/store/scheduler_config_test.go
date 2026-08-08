package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestGetSchedulerConfig_DefaultsWhenUnset(t *testing.T) {
	s := openTestStore(t)
	cfg, err := s.GetSchedulerConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSchedulerConfig() error: %v", err)
	}
	if !reflect.DeepEqual(cfg, DefaultSchedulerConfig()) {
		t.Errorf("GetSchedulerConfig() = %+v, want %+v", cfg, DefaultSchedulerConfig())
	}
}

func TestUpdateSchedulerConfig_RoundTrips(t *testing.T) {
	s := openTestStore(t)
	budget := 12.5
	want := SchedulerConfig{
		Enabled:        true,
		Aggressiveness: 75,
		ReservedBlocks: []TimeBlock{
			{Days: []time.Weekday{time.Monday, time.Tuesday}, StartMin: 9 * 60, EndMin: 17 * 60},
		},
		PreferredWindows: []TimeBlock{
			{StartMin: 22 * 60, EndMin: 6 * 60}, // wraps midnight
		},
		MaxBudgetUSD: &budget,
	}

	if err := s.UpdateSchedulerConfig(context.Background(), want); err != nil {
		t.Fatalf("UpdateSchedulerConfig() error: %v", err)
	}

	got, err := s.GetSchedulerConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSchedulerConfig() error: %v", err)
	}
	if got.Enabled != want.Enabled || got.Aggressiveness != want.Aggressiveness {
		t.Errorf("got Enabled=%v Aggressiveness=%v, want Enabled=%v Aggressiveness=%v",
			got.Enabled, got.Aggressiveness, want.Enabled, want.Aggressiveness)
	}
	if len(got.ReservedBlocks) != 1 || got.ReservedBlocks[0].StartMin != 9*60 {
		t.Errorf("ReservedBlocks round-trip mismatch: %+v", got.ReservedBlocks)
	}
	if len(got.PreferredWindows) != 1 || got.PreferredWindows[0].EndMin != 6*60 {
		t.Errorf("PreferredWindows round-trip mismatch: %+v", got.PreferredWindows)
	}
	if got.MaxBudgetUSD == nil || *got.MaxBudgetUSD != budget {
		t.Errorf("MaxBudgetUSD = %v, want %v", got.MaxBudgetUSD, budget)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero after update")
	}
}

func TestUpdateSchedulerConfig_UpsertOverwrites(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.UpdateSchedulerConfig(ctx, SchedulerConfig{Enabled: true, Aggressiveness: 30}); err != nil {
		t.Fatalf("first UpdateSchedulerConfig() error: %v", err)
	}
	if err := s.UpdateSchedulerConfig(ctx, SchedulerConfig{Enabled: false, Aggressiveness: 90}); err != nil {
		t.Fatalf("second UpdateSchedulerConfig() error: %v", err)
	}

	got, err := s.GetSchedulerConfig(ctx)
	if err != nil {
		t.Fatalf("GetSchedulerConfig() error: %v", err)
	}
	if got.Enabled || got.Aggressiveness != 90 {
		t.Errorf("got %+v, want Enabled=false Aggressiveness=90", got)
	}
}

func TestUpdateMaxBudgetUSD_LeavesOtherFieldsAlone(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.UpdateSchedulerConfig(ctx, SchedulerConfig{Enabled: true, Aggressiveness: 60}); err != nil {
		t.Fatalf("UpdateSchedulerConfig() error: %v", err)
	}

	budget := 5.0
	if err := s.UpdateMaxBudgetUSD(ctx, &budget); err != nil {
		t.Fatalf("UpdateMaxBudgetUSD() error: %v", err)
	}

	got, err := s.GetSchedulerConfig(ctx)
	if err != nil {
		t.Fatalf("GetSchedulerConfig() error: %v", err)
	}
	if !got.Enabled || got.Aggressiveness != 60 {
		t.Errorf("UpdateMaxBudgetUSD changed unrelated fields: %+v", got)
	}
	if got.MaxBudgetUSD == nil || *got.MaxBudgetUSD != budget {
		t.Errorf("MaxBudgetUSD = %v, want %v", got.MaxBudgetUSD, budget)
	}

	if err := s.UpdateMaxBudgetUSD(ctx, nil); err != nil {
		t.Fatalf("UpdateMaxBudgetUSD(nil) error: %v", err)
	}
	got, err = s.GetSchedulerConfig(ctx)
	if err != nil {
		t.Fatalf("GetSchedulerConfig() error: %v", err)
	}
	if got.MaxBudgetUSD != nil {
		t.Errorf("MaxBudgetUSD = %v, want nil", got.MaxBudgetUSD)
	}
}

func TestTimeBlock_Contains_NonWrapping(t *testing.T) {
	b := TimeBlock{StartMin: 9 * 60, EndMin: 17 * 60}
	loc := time.UTC

	in := time.Date(2026, 8, 10, 12, 0, 0, 0, loc) // Monday noon
	if !b.Contains(in) {
		t.Errorf("Contains(%v) = false, want true", in)
	}

	out := time.Date(2026, 8, 10, 20, 0, 0, 0, loc)
	if b.Contains(out) {
		t.Errorf("Contains(%v) = true, want false", out)
	}
}

func TestTimeBlock_Contains_Wraparound(t *testing.T) {
	b := TimeBlock{StartMin: 22 * 60, EndMin: 6 * 60} // 22:00-06:00
	loc := time.UTC

	lateNight := time.Date(2026, 8, 10, 23, 30, 0, 0, loc)
	if !b.Contains(lateNight) {
		t.Errorf("Contains(%v) = false, want true (late side of wrap)", lateNight)
	}

	earlyMorning := time.Date(2026, 8, 11, 3, 0, 0, 0, loc)
	if !b.Contains(earlyMorning) {
		t.Errorf("Contains(%v) = false, want true (early side of wrap)", earlyMorning)
	}

	midday := time.Date(2026, 8, 11, 12, 0, 0, 0, loc)
	if b.Contains(midday) {
		t.Errorf("Contains(%v) = true, want false", midday)
	}
}

func TestTimeBlock_Contains_RestrictedToDays(t *testing.T) {
	b := TimeBlock{Days: []time.Weekday{time.Saturday, time.Sunday}, StartMin: 0, EndMin: 24 * 60}

	saturday := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	if !b.Contains(saturday) {
		t.Errorf("Contains(%v) = false, want true", saturday)
	}

	monday := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	if b.Contains(monday) {
		t.Errorf("Contains(%v) = true, want false", monday)
	}
}
