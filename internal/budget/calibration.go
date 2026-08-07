package budget

import (
	"context"
	"fmt"
	"time"
)

// CalibrationEstimate is a learned tokens-per-percent rate for one
// rate-limit window, fit from paired (ground-truth used_percentage delta,
// ledger tokens spent between the two readings) observations
// (REQUIREMENTS.md §6.1 item 3). Per §6.3, a single delta is never
// trusted: Samples must reach minCalibrationSamples before
// TokensPerPercent is usable — below that, Insufficient is true and
// TokensPerPercent is 0. Cold-start prior selection (REQUIREMENTS.md §10
// open question 3) is deliberately not decided here: a caller that sees
// Insufficient falls back to raw ledger/ground-truth totals, exactly as
// it did before calibration existed.
type CalibrationEstimate struct {
	TokensPerPercent float64
	Samples          int
	Insufficient     bool
}

// minCalibrationSamples is the minimum number of qualifying reading pairs
// before a fit is considered usable, per §6.3's "never a single delta".
const minCalibrationSamples = 3

// CalibrateFiveHour fits TokensPerPercent for the five-hour window from
// every pair of consecutive ground-truth readings that share the same
// FiveHourResetsAt (so the delta reflects spend within one window, not a
// rollover) and show a positive percentage increase. Tokens spent between
// a pair is the sum of InputTokens+OutputTokens from every ledger entry
// recorded in (prev.ObservedAt, cur.ObservedAt]. TokensPerPercent is the
// mean of tokens_spent/delta_percent across all qualifying pairs.
func (l *Ledger) CalibrateFiveHour(ctx context.Context) (CalibrationEstimate, error) {
	readings, err := l.store.ListGroundTruth(ctx)
	if err != nil {
		return CalibrationEstimate{}, fmt.Errorf("listing ground truth readings: %w", err)
	}

	var ratios []float64
	for i := 1; i < len(readings); i++ {
		prev, cur := readings[i-1], readings[i]
		if !prev.FiveHourResetsAt.Equal(cur.FiveHourResetsAt) {
			continue
		}
		deltaPct := cur.FiveHourUsedPercentage - prev.FiveHourUsedPercentage
		if deltaPct <= 0 {
			continue
		}
		tokensSpent, err := l.tokensSpentBetween(ctx, prev.ObservedAt, cur.ObservedAt)
		if err != nil {
			return CalibrationEstimate{}, err
		}
		if tokensSpent == 0 {
			continue
		}
		ratios = append(ratios, float64(tokensSpent)/float64(deltaPct))
	}
	return meanEstimate(ratios), nil
}

// CalibrateSevenDay is CalibrateFiveHour's seven-day-window counterpart.
func (l *Ledger) CalibrateSevenDay(ctx context.Context) (CalibrationEstimate, error) {
	readings, err := l.store.ListGroundTruth(ctx)
	if err != nil {
		return CalibrationEstimate{}, fmt.Errorf("listing ground truth readings: %w", err)
	}

	var ratios []float64
	for i := 1; i < len(readings); i++ {
		prev, cur := readings[i-1], readings[i]
		if !prev.SevenDayResetsAt.Equal(cur.SevenDayResetsAt) {
			continue
		}
		deltaPct := cur.SevenDayUsedPercentage - prev.SevenDayUsedPercentage
		if deltaPct <= 0 {
			continue
		}
		tokensSpent, err := l.tokensSpentBetween(ctx, prev.ObservedAt, cur.ObservedAt)
		if err != nil {
			return CalibrationEstimate{}, err
		}
		if tokensSpent == 0 {
			continue
		}
		ratios = append(ratios, float64(tokensSpent)/float64(deltaPct))
	}
	return meanEstimate(ratios), nil
}

// tokensSpentBetween sums InputTokens+OutputTokens for every ledger entry
// recorded in (since, until].
func (l *Ledger) tokensSpentBetween(ctx context.Context, since, until time.Time) (int, error) {
	entries, err := l.store.ListUsageSince(ctx, since)
	if err != nil {
		return 0, fmt.Errorf("listing usage since %s: %w", since, err)
	}
	var total int
	for _, e := range entries {
		if e.RecordedAt.After(until) {
			continue
		}
		total += e.InputTokens + e.OutputTokens
	}
	return total, nil
}

func meanEstimate(ratios []float64) CalibrationEstimate {
	if len(ratios) < minCalibrationSamples {
		return CalibrationEstimate{Samples: len(ratios), Insufficient: true}
	}
	var sum float64
	for _, r := range ratios {
		sum += r
	}
	return CalibrationEstimate{TokensPerPercent: sum / float64(len(ratios)), Samples: len(ratios)}
}
