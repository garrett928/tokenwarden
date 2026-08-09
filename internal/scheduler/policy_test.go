package scheduler

import (
	"testing"
	"time"

	"tokenwarden/internal/store"
)

func TestDecide_Disabled(t *testing.T) {
	cfg := store.SchedulerConfig{Enabled: false, Aggressiveness: 80}
	got := Decide(cfg, UsageState{}, time.Now())
	if got.Action != ActionDisabled {
		t.Errorf("Action = %v, want ActionDisabled", got.Action)
	}
}

func TestDecide_ReservedBlock(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) // Monday noon
	cfg := store.SchedulerConfig{
		Enabled:        true,
		Aggressiveness: 80,
		ReservedBlocks: []store.TimeBlock{{StartMin: 9 * 60, EndMin: 17 * 60}},
	}
	got := Decide(cfg, UsageState{}, now)
	if got.Action != ActionReservedBlock {
		t.Errorf("Action = %v, want ActionReservedBlock", got.Action)
	}
}

func TestDecide_FiveHourCeiling(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	resetsAt := now.Add(2 * time.Hour)
	cfg := store.SchedulerConfig{Enabled: true, Aggressiveness: 80}
	usage := UsageState{FiveHourUsedPercent: 79, FiveHourResetsAt: resetsAt}

	got := Decide(cfg, usage, now)
	if got.Action != ActionFiveHourCeiling {
		t.Fatalf("Action = %v, want ActionFiveHourCeiling", got.Action)
	}
	if !got.SleepUntil.Equal(resetsAt) {
		t.Errorf("SleepUntil = %v, want %v", got.SleepUntil, resetsAt)
	}
}

func TestDecide_FiveHourCeiling_SafetyMargin(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cfg := store.SchedulerConfig{Enabled: true, Aggressiveness: 80}

	// 2% below aggressiveness is still within the safety margin -> holds.
	held := Decide(cfg, UsageState{FiveHourUsedPercent: 78}, now)
	if held.Action != ActionFiveHourCeiling {
		t.Errorf("at 78%% used with 80%% aggressiveness, Action = %v, want ActionFiveHourCeiling (safety margin)", held.Action)
	}

	// Comfortably below -> dispatch allowed.
	allowed := Decide(cfg, UsageState{FiveHourUsedPercent: 50}, now)
	if allowed.Action != ActionDispatch {
		t.Errorf("at 50%% used, Action = %v, want ActionDispatch", allowed.Action)
	}
}

func TestDecide_WeeklyTarget(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	resetsAt := now.Add(3 * 24 * time.Hour)
	cfg := store.SchedulerConfig{Enabled: true, Aggressiveness: 60}
	usage := UsageState{FiveHourUsedPercent: 0, SevenDayUsedPercent: 58, SevenDayResetsAt: resetsAt}

	got := Decide(cfg, usage, now)
	if got.Action != ActionWeeklyTarget {
		t.Fatalf("Action = %v, want ActionWeeklyTarget", got.Action)
	}
	if !got.SleepUntil.Equal(resetsAt) {
		t.Errorf("SleepUntil = %v, want %v", got.SleepUntil, resetsAt)
	}
}

func TestDecide_ZeroAggressiveness_NeverDispatches(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cfg := store.SchedulerConfig{Enabled: true, Aggressiveness: 0}
	got := Decide(cfg, UsageState{}, now)
	if got.Action != ActionFiveHourCeiling {
		t.Errorf("Action = %v, want ActionFiveHourCeiling at 0%% aggressiveness", got.Action)
	}
}

func TestDecide_AllClear_Dispatches(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cfg := store.SchedulerConfig{Enabled: true, Aggressiveness: 80}
	usage := UsageState{FiveHourUsedPercent: 10, SevenDayUsedPercent: 10}

	got := Decide(cfg, usage, now)
	if got.Action != ActionDispatch {
		t.Errorf("Action = %v, want ActionDispatch", got.Action)
	}
}
