package scheduler

import (
	"context"
	"time"

	"tokenwarden/internal/budget"
)

// UsageSource records where a UsageState's percentages came from, so
// callers (and tests) can distinguish an authoritative reading from a
// dead-reckoned one — REQUIREMENTS.md §6.3 requires the engine never treat
// an estimate as precise as ground truth.
type UsageSource string

const (
	SourceGroundTruth UsageSource = "ground_truth"
	SourceCalibrated  UsageSource = "calibrated"
	SourceUnknown     UsageSource = "unknown"
)

// UsageState is one tick's fused view of window fill (REQUIREMENTS.md
// §6.1's sensor fusion, §6.2 step 1).
type UsageState struct {
	FiveHourUsedPercent int
	FiveHourResetsAt    time.Time
	SevenDayUsedPercent int
	SevenDayResetsAt    time.Time
	Source              UsageSource
}

// groundTruthFreshness bounds how long a ground-truth reading is trusted
// before usage state falls back to calibration/ledger. Twice the fixed
// 30-minute sentinel cadence (REQUIREMENTS.md §10 item 1's resolution), so
// one missed or delayed sentinel firing doesn't immediately discard an
// otherwise-still-reasonable reading.
const groundTruthFreshness = 60 * time.Minute

// computeUsageState implements §6.2 step 1: use the latest ground-truth
// reading if it's fresh, otherwise fall back to the ledger converted
// through calibration (§6.1 items 2-3). When neither a fresh reading nor a
// sufficient calibration exists, the returned percentages are 0 and
// Source is SourceUnknown — callers must not treat that as "window empty",
// only as "no signal available yet" (REQUIREMENTS.md §10 open question 3's
// resolution: no prior, don't guess).
func computeUsageState(ctx context.Context, ledger *budget.Ledger, now time.Time) (UsageState, error) {
	reading, haveReading, err := ledger.LatestGroundTruth(ctx)
	if err != nil {
		return UsageState{}, err
	}
	if haveReading && now.Sub(reading.ObservedAt) <= groundTruthFreshness {
		return UsageState{
			FiveHourUsedPercent: reading.FiveHourUsedPercentage,
			FiveHourResetsAt:    reading.FiveHourResetsAt,
			SevenDayUsedPercent: reading.SevenDayUsedPercentage,
			SevenDayResetsAt:    reading.SevenDayResetsAt,
			Source:              SourceGroundTruth,
		}, nil
	}

	fiveHour, err := ledger.FiveHourTotal(ctx, now)
	if err != nil {
		return UsageState{}, err
	}
	sevenDay, err := ledger.SevenDayTotal(ctx, now)
	if err != nil {
		return UsageState{}, err
	}
	fiveHourCal, err := ledger.CalibrateFiveHour(ctx)
	if err != nil {
		return UsageState{}, err
	}
	sevenDayCal, err := ledger.CalibrateSevenDay(ctx)
	if err != nil {
		return UsageState{}, err
	}

	state := UsageState{Source: SourceUnknown}
	if haveReading {
		// A stale reading is still the best information available about
		// when the windows actually reset.
		state.FiveHourResetsAt = reading.FiveHourResetsAt
		state.SevenDayResetsAt = reading.SevenDayResetsAt
	} else {
		state.FiveHourResetsAt = now.Add(fiveHourWindowDuration)
		state.SevenDayResetsAt = now.Add(sevenDayWindowDuration)
	}

	if !fiveHourCal.Insufficient && fiveHourCal.TokensPerPercent > 0 {
		state.FiveHourUsedPercent = percentFromTokens(fiveHour.InputTokens+fiveHour.OutputTokens, fiveHourCal.TokensPerPercent)
		state.Source = SourceCalibrated
	}
	if !sevenDayCal.Insufficient && sevenDayCal.TokensPerPercent > 0 {
		state.SevenDayUsedPercent = percentFromTokens(sevenDay.InputTokens+sevenDay.OutputTokens, sevenDayCal.TokensPerPercent)
		if state.Source == SourceUnknown {
			state.Source = SourceCalibrated
		}
	}
	return state, nil
}

const (
	fiveHourWindowDuration = 5 * time.Hour
	sevenDayWindowDuration = 7 * 24 * time.Hour
)

func percentFromTokens(tokens int, tokensPerPercent float64) int {
	if tokensPerPercent <= 0 {
		return 0
	}
	pct := int(float64(tokens) / tokensPerPercent)
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}
