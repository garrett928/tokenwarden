package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GroundTruthReading is one capture of the plan's actual rate_limits
// state (REQUIREMENTS.md §6.1 item 1) — the only authoritative source of
// window-fill state tokenwarden has. FiveHour/SevenDay percentages are
// integers: SPIKE-001 conclusion 4 found the CLI reports 1% resolution,
// not the float the docs example implies.
type GroundTruthReading struct {
	ID                     string
	FiveHourUsedPercentage int
	FiveHourResetsAt       time.Time
	SevenDayUsedPercentage int
	SevenDayResetsAt       time.Time
	ObservedAt             time.Time
}

// RecordGroundTruth persists a reading. It assigns an ID if one isn't set
// and stamps ObservedAt with the current time if it's zero.
func (s *Store) RecordGroundTruth(ctx context.Context, r GroundTruthReading) (GroundTruthReading, error) {
	if r.ID == "" {
		id, err := randomID("gt")
		if err != nil {
			return GroundTruthReading{}, err
		}
		r.ID = id
	}
	if r.ObservedAt.IsZero() {
		r.ObservedAt = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ground_truth_readings (
			id, five_hour_used_percentage, five_hour_resets_at,
			seven_day_used_percentage, seven_day_resets_at, observed_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`,
		r.ID, r.FiveHourUsedPercentage, r.FiveHourResetsAt.Unix(),
		r.SevenDayUsedPercentage, r.SevenDayResetsAt.Unix(), r.ObservedAt.Unix(),
	)
	if err != nil {
		return GroundTruthReading{}, fmt.Errorf("inserting ground truth reading: %w", err)
	}
	return r, nil
}

// LatestGroundTruth returns the most recently observed reading, or
// ErrNotFound if none has ever been recorded.
func (s *Store) LatestGroundTruth(ctx context.Context) (GroundTruthReading, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, five_hour_used_percentage, five_hour_resets_at,
			seven_day_used_percentage, seven_day_resets_at, observed_at
		FROM ground_truth_readings
		ORDER BY observed_at DESC
		LIMIT 1
	`)

	var (
		r                                      GroundTruthReading
		fiveHourResetsUnix, sevenDayResetsUnix int64
		observedAtUnix                         int64
	)
	err := row.Scan(
		&r.ID, &r.FiveHourUsedPercentage, &fiveHourResetsUnix,
		&r.SevenDayUsedPercentage, &sevenDayResetsUnix, &observedAtUnix,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return GroundTruthReading{}, ErrNotFound
	}
	if err != nil {
		return GroundTruthReading{}, fmt.Errorf("getting latest ground truth reading: %w", err)
	}

	r.FiveHourResetsAt = time.Unix(fiveHourResetsUnix, 0).UTC()
	r.SevenDayResetsAt = time.Unix(sevenDayResetsUnix, 0).UTC()
	r.ObservedAt = time.Unix(observedAtUnix, 0).UTC()
	return r, nil
}
