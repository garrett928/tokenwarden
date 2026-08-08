package scheduler

import (
	"time"

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
	ceiling := cfg.Aggressiveness - safetyMarginPercent
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
