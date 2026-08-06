package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tokenwarden/internal/store"
)

// GroundTruthReading is one capture of the plan's actual rate_limits state
// (REQUIREMENTS.md §6.1 item 1) — the only authoritative source of window
// fill tokenwarden has. Percentages are integers per SPIKE-001 conclusion
// 4 (§6.3): 1% resolution, not a float.
type GroundTruthReading struct {
	FiveHourUsedPercentage int
	FiveHourResetsAt       time.Time
	SevenDayUsedPercentage int
	SevenDayResetsAt       time.Time
	ObservedAt             time.Time
}

// RecordGroundTruth persists a reading captured by the twprobe statusline
// shim (passive path — the shim is installed globally so the user's own
// interactive sessions feed the daemon for free) or, in a future slice, a
// periodic PTY sentinel (active path). Every reading is meant to "snap"
// whatever dead-reckoned estimate a future calibration layer produces;
// this slice only stores the reading — the snapping/calibration logic
// itself doesn't exist yet.
func (l *Ledger) RecordGroundTruth(ctx context.Context, r GroundTruthReading) error {
	_, err := l.store.RecordGroundTruth(ctx, store.GroundTruthReading{
		FiveHourUsedPercentage: r.FiveHourUsedPercentage,
		FiveHourResetsAt:       r.FiveHourResetsAt,
		SevenDayUsedPercentage: r.SevenDayUsedPercentage,
		SevenDayResetsAt:       r.SevenDayResetsAt,
		ObservedAt:             r.ObservedAt,
	})
	if err != nil {
		return fmt.Errorf("recording ground truth reading: %w", err)
	}
	return nil
}

// LatestGroundTruth returns the most recently observed reading. ok is
// false when no reading has ever been recorded — a legitimate, expected
// state before the twprobe shim has fired at least once, not an error.
func (l *Ledger) LatestGroundTruth(ctx context.Context) (reading GroundTruthReading, ok bool, err error) {
	r, err := l.store.LatestGroundTruth(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return GroundTruthReading{}, false, nil
	}
	if err != nil {
		return GroundTruthReading{}, false, fmt.Errorf("getting latest ground truth reading: %w", err)
	}
	return GroundTruthReading{
		FiveHourUsedPercentage: r.FiveHourUsedPercentage,
		FiveHourResetsAt:       r.FiveHourResetsAt,
		SevenDayUsedPercentage: r.SevenDayUsedPercentage,
		SevenDayResetsAt:       r.SevenDayResetsAt,
		ObservedAt:             r.ObservedAt,
	}, true, nil
}
