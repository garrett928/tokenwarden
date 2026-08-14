package scheduler

import (
	"fmt"
	"time"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/store"
)

// safetyMarginPercent is the minimum headroom kept below any ceiling.
// REQUIREMENTS.md §6.3: "safety margin is at least 2x the quantisation
// error" and used_percentage's quantisation is 1% (§3.2), so >=2%.
const safetyMarginPercent = 2

// ActionKind is what Decide told a tick to do.
type ActionKind int

const (
	// ActionDispatch means nothing is holding the loop back this tick —
	// Engine should pick and dispatch the next runnable job.
	ActionDispatch ActionKind = iota
	// ActionDisabled means the scheduler config has Enabled=false.
	ActionDisabled
	// ActionReservedBlock means now falls inside a configured reserved
	// block (FR-SCHED-2).
	ActionReservedBlock
	// ActionFiveHourCeiling means the five-hour window has reached the
	// aggressiveness ceiling (§6.2 step 3); dispatch should hold off until
	// SleepUntil.
	ActionFiveHourCeiling
	// ActionWeeklyTarget means the seven-day window has reached the
	// weekly target (§6.2 step 4); dispatch should hold off until
	// SleepUntil.
	ActionWeeklyTarget
)

// String names an ActionKind for logging.
func (a ActionKind) String() string {
	switch a {
	case ActionDispatch:
		return "dispatch"
	case ActionDisabled:
		return "disabled"
	case ActionReservedBlock:
		return "reserved_block"
	case ActionFiveHourCeiling:
		return "five_hour_ceiling"
	case ActionWeeklyTarget:
		return "weekly_target"
	default:
		return "unknown"
	}
}

// Decision is Decide's verdict for one tick.
type Decision struct {
	Action ActionKind
	Reason string
	// SleepUntil is meaningful for ActionFiveHourCeiling and
	// ActionWeeklyTarget: the window's resets_at, when dispatch may
	// resume. Zero for every other Action.
	SleepUntil time.Time
}

// Decide implements REQUIREMENTS.md §6.2 steps 2-4: whether this tick
// should dispatch nothing, and why. It never itself picks or runs a job —
// Engine does that when Decide returns ActionDispatch. cfg.Enabled=false
// short-circuits before any of the window checks.
func Decide(cfg store.SchedulerConfig, usage UsageState, now time.Time) Decision {
	if !cfg.Enabled {
		return Decision{Action: ActionDisabled, Reason: "scheduler is disabled"}
	}

	for _, b := range cfg.ReservedBlocks {
		if b.Contains(now) {
			return Decision{Action: ActionReservedBlock, Reason: "inside a reserved block"}
		}
	}

	// FR-SCHED-1: aggressiveness governs both the 5-hour ceiling and the
	// weekly target — one knob for "how much of weekly capacity to target
	// and how full to let the 5-hour window get."
	ceiling := fiveHourCeilingPercent(cfg)
	if usage.FiveHourUsedPercent >= ceiling {
		return Decision{
			Action:     ActionFiveHourCeiling,
			Reason:     "five-hour window at aggressiveness ceiling",
			SleepUntil: usage.FiveHourResetsAt,
		}
	}

	weeklyTarget := cfg.Aggressiveness - safetyMarginPercent
	if usage.SevenDayUsedPercent >= weeklyTarget {
		return Decision{
			Action:     ActionWeeklyTarget,
			Reason:     "seven-day window at weekly target",
			SleepUntil: usage.SevenDayResetsAt,
		}
	}

	return Decision{Action: ActionDispatch}
}

// fiveHourCeilingPercent is the used_percentage past which the five-hour
// window stops accepting new dispatches (§6.2 step 3, §6.3's safety
// margin). Shared by Decide and FitJob so both apply exactly the same
// ceiling.
func fiveHourCeilingPercent(cfg store.SchedulerConfig) int {
	return cfg.Aggressiveness - safetyMarginPercent
}

// FitAction is FitJob's verdict for one candidate job.
type FitAction int

const (
	// FitDispatch means the job should be dispatched as-is: either its
	// predicted cost fits remaining 5-hour headroom, or no prediction is
	// available yet (cold start, §10 open question 3 — don't guess, don't
	// block).
	FitDispatch FitAction = iota
	// FitCapBudget means the job doesn't fit, but is Resumable: cap its
	// budget to remaining headroom and dispatch it anyway (§6.4 strategy 1).
	FitCapBudget
	// FitDefer means the job doesn't fit and can't be fit by any strategy
	// this slice implements: DeferOversized it and let the caller try the
	// next-best candidate (§6.4 strategy 4, §6.2 step 7).
	FitDefer
)

// FitResult is FitJob's verdict plus the reasoning/parameters the caller
// needs to act on it.
type FitResult struct {
	Action FitAction
	Reason string
	// BudgetCapUSD is meaningful only for FitCapBudget: the USD cap to set
	// via queue.CapBudget before dispatching.
	BudgetCapUSD float64
}

// FitJob implements REQUIREMENTS.md §6.4 for one candidate job: whether its
// predicted cost fits the five-hour window's remaining headroom, and if
// not, which of the implemented strategies (1: budget-capped continuation,
// 4: defer) applies. Declared-steps promotion (strategy 2) and
// model-driven decomposition (strategy 3) aren't implemented yet — see the
// package doc — so a job that doesn't fit and isn't Resumable always
// defers rather than attempting either.
//
// Per §10 open question 3's resolution, an insufficient cost prediction or
// five-hour calibration means "no signal", not "assume it fits by
// default" and not "assume it's oversized" — FitJob returns FitDispatch
// and relies on the hard ceiling check (Decide, already run before this)
// to have gated dispatch in the first place.
func FitJob(job store.Job, prediction budget.CostPrediction, cal budget.CalibrationEstimate, cfg store.SchedulerConfig, fiveHourUsedPercent int) FitResult {
	if prediction.Insufficient || cal.Insufficient || cal.TokensPerPercent <= 0 {
		return FitResult{Action: FitDispatch, Reason: "no cost prediction available yet (cold start)"}
	}

	ceiling := fiveHourCeilingPercent(cfg)
	predictedPercent := float64(prediction.Tokens) / cal.TokensPerPercent
	remainingPercent := float64(ceiling - fiveHourUsedPercent)
	if predictedPercent <= remainingPercent {
		return FitResult{Action: FitDispatch}
	}

	if !job.Resumable {
		return FitResult{
			Action: FitDefer,
			Reason: fmt.Sprintf("predicted cost (~%.0f%% of the five-hour window) exceeds %.0f%% remaining headroom, and job is not resumable", predictedPercent, remainingPercent),
		}
	}
	if remainingPercent <= 0 || prediction.Tokens <= 0 {
		return FitResult{Action: FitDefer, Reason: "no five-hour headroom remains to cap a budget against"}
	}

	costPerToken := prediction.CostUSD / float64(prediction.Tokens)
	budgetCap := remainingPercent * cal.TokensPerPercent * costPerToken
	if budgetCap <= 0 {
		return FitResult{Action: FitDefer, Reason: "predicted budget cap for remaining headroom rounds to zero"}
	}

	return FitResult{
		Action:       FitCapBudget,
		Reason:       fmt.Sprintf("predicted cost (~%.0f%% of the five-hour window) exceeds %.0f%% remaining headroom; capping to ~$%.2f and resuming next window", predictedPercent, remainingPercent, budgetCap),
		BudgetCapUSD: budgetCap,
	}
}
