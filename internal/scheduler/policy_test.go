package scheduler

import (
	"math"
	"testing"
	"time"

	"tokenwarden/internal/budget"
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

// fitCfg is 80% aggressiveness -> a 78% five-hour ceiling (safetyMarginPercent).
var fitCfg = store.SchedulerConfig{Enabled: true, Aggressiveness: 80}

func TestFitJob_ColdStart_InsufficientPrediction_Dispatches(t *testing.T) {
	job := store.Job{Resumable: false}
	prediction := budget.CostPrediction{Insufficient: true}
	cal := budget.CalibrationEstimate{TokensPerPercent: 1000}

	got := FitJob(job, prediction, cal, fitCfg, 70)
	if got.Action != FitDispatch {
		t.Errorf("Action = %v, want FitDispatch (no cost prediction yet, don't block on a guess)", got.Action)
	}
}

func TestFitJob_ColdStart_InsufficientCalibration_Dispatches(t *testing.T) {
	job := store.Job{Resumable: false}
	prediction := budget.CostPrediction{Tokens: 10000, CostUSD: 2, Samples: 5}
	cal := budget.CalibrationEstimate{Insufficient: true}

	got := FitJob(job, prediction, cal, fitCfg, 70)
	if got.Action != FitDispatch {
		t.Errorf("Action = %v, want FitDispatch (no calibration yet, can't convert tokens to %% headroom)", got.Action)
	}
}

func TestFitJob_PredictedCostFitsHeadroom_Dispatches(t *testing.T) {
	job := store.Job{Resumable: false}
	// 5000 tokens / 1000 tokens-per-percent = 5%, well inside the 8%
	// remaining headroom (78% ceiling - 70% used).
	prediction := budget.CostPrediction{Tokens: 5000, CostUSD: 1, Samples: 5}
	cal := budget.CalibrationEstimate{TokensPerPercent: 1000, Samples: 5}

	got := FitJob(job, prediction, cal, fitCfg, 70)
	if got.Action != FitDispatch {
		t.Errorf("Action = %v, want FitDispatch (predicted 5%% fits 8%% remaining headroom)", got.Action)
	}
}

func TestFitJob_OversizedAndNotResumable_Defers(t *testing.T) {
	job := store.Job{Resumable: false}
	// 10000 tokens / 1000 = 10%, exceeds the 8% remaining headroom.
	prediction := budget.CostPrediction{Tokens: 10000, CostUSD: 2, Samples: 5}
	cal := budget.CalibrationEstimate{TokensPerPercent: 1000, Samples: 5}

	got := FitJob(job, prediction, cal, fitCfg, 70)
	if got.Action != FitDefer {
		t.Errorf("Action = %v, want FitDefer (oversized, not resumable, no fitting strategy applies)", got.Action)
	}
	if got.Reason == "" {
		t.Error("Reason is empty, want an explanation for the deferral")
	}
}

func TestFitJob_OversizedAndResumable_CapsBudget(t *testing.T) {
	job := store.Job{Resumable: true}
	// 10000 tokens / 1000 = 10%, exceeds the 8% remaining headroom.
	// costPerToken = 2/10000 = 0.0002; remaining 8% * 1000 tokens/% = 8000
	// tokens; budget cap = 8000 * 0.0002 = 1.6.
	prediction := budget.CostPrediction{Tokens: 10000, CostUSD: 2, Samples: 5}
	cal := budget.CalibrationEstimate{TokensPerPercent: 1000, Samples: 5}

	got := FitJob(job, prediction, cal, fitCfg, 70)
	if got.Action != FitCapBudget {
		t.Fatalf("Action = %v, want FitCapBudget (oversized but resumable)", got.Action)
	}
	if math.Abs(got.BudgetCapUSD-1.6) > 1e-9 {
		t.Errorf("BudgetCapUSD = %v, want ~1.6", got.BudgetCapUSD)
	}
}

func TestFitJob_OversizedResumableNoHeadroomLeft_Defers(t *testing.T) {
	job := store.Job{Resumable: true}
	prediction := budget.CostPrediction{Tokens: 10000, CostUSD: 2, Samples: 5}
	cal := budget.CalibrationEstimate{TokensPerPercent: 1000, Samples: 5}

	// Already at the 78% ceiling -> zero remaining headroom to cap against.
	got := FitJob(job, prediction, cal, fitCfg, 78)
	if got.Action != FitDefer {
		t.Errorf("Action = %v, want FitDefer (no headroom left even though resumable)", got.Action)
	}
}
