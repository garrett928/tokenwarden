package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TimeBlock is a recurring time-of-day range used for reserved blocks
// (FR-SCHED-2) and preferred windows (FR-SCHED-3). Days restricts which
// weekdays the block applies to; an empty Days means every day. StartMin
// and EndMin are minutes since local midnight, [0, 1440). EndMin <=
// StartMin means the block wraps past midnight (e.g. 22:00-06:00), rather
// than being an empty or invalid range.
type TimeBlock struct {
	Days     []time.Weekday `json:"days,omitempty"`
	StartMin int            `json:"start_min"`
	EndMin   int            `json:"end_min"`
}

// Contains reports whether t falls inside b, evaluated in t's own
// location. A wraparound block (EndMin <= StartMin) is checked against
// both the day t falls on and the day before it, since the tail end of a
// block that started "yesterday" (in block-days terms) can still cover a
// timestamp after local midnight.
func (b TimeBlock) Contains(t time.Time) bool {
	minOfDay := t.Hour()*60 + t.Minute()
	wraps := b.EndMin <= b.StartMin

	if !wraps {
		return b.appliesToDay(t.Weekday()) && minOfDay >= b.StartMin && minOfDay < b.EndMin
	}

	// Wraparound: either we're in the late part of a block that started
	// today, or in the early part of a block that started yesterday.
	if b.appliesToDay(t.Weekday()) && minOfDay >= b.StartMin {
		return true
	}
	yesterday := t.Add(-24 * time.Hour).Weekday()
	if b.appliesToDay(yesterday) && minOfDay < b.EndMin {
		return true
	}
	return false
}

func (b TimeBlock) appliesToDay(d time.Weekday) bool {
	if len(b.Days) == 0 {
		return true
	}
	for _, day := range b.Days {
		if day == d {
			return true
		}
	}
	return false
}

// SchedulerConfig is the dispatch loop's persisted policy (REQUIREMENTS.md
// §5.2, §6.2). Aggressiveness (FR-SCHED-1) governs both the 5-hour
// ceiling and the weekly target — a single knob for "how much of weekly
// capacity to target and how full to let the 5-hour window get."
type SchedulerConfig struct {
	Enabled          bool
	Aggressiveness   int // 0-100, FR-SCHED-1
	ReservedBlocks   []TimeBlock
	PreferredWindows []TimeBlock
	MaxBudgetUSD     *float64
	UpdatedAt        time.Time
}

// DefaultSchedulerConfig is returned by GetSchedulerConfig before any
// config has ever been saved: disabled, so the scheduler never dispatches
// until a user explicitly opts in and sets an aggressiveness.
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{Enabled: false, Aggressiveness: 50}
}

// GetSchedulerConfig returns the single persisted scheduler config, or
// DefaultSchedulerConfig if none has been saved yet — an expected startup
// state, not an error.
func (s *Store) GetSchedulerConfig(ctx context.Context) (SchedulerConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT enabled, aggressiveness, reserved_blocks, preferred_windows, max_budget_usd, updated_at
		FROM scheduler_config WHERE id = 1
	`)

	var (
		cfg                                SchedulerConfig
		enabledInt                         int
		reservedJSON, preferredJSON        string
		maxBudget                          sql.NullFloat64
		updatedAtUnix                      int64
	)
	err := row.Scan(&enabledInt, &cfg.Aggressiveness, &reservedJSON, &preferredJSON, &maxBudget, &updatedAtUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSchedulerConfig(), nil
	}
	if err != nil {
		return SchedulerConfig{}, fmt.Errorf("getting scheduler config: %w", err)
	}

	cfg.Enabled = enabledInt != 0
	if err := json.Unmarshal([]byte(reservedJSON), &cfg.ReservedBlocks); err != nil {
		return SchedulerConfig{}, fmt.Errorf("decoding reserved_blocks: %w", err)
	}
	if err := json.Unmarshal([]byte(preferredJSON), &cfg.PreferredWindows); err != nil {
		return SchedulerConfig{}, fmt.Errorf("decoding preferred_windows: %w", err)
	}
	if maxBudget.Valid {
		v := maxBudget.Float64
		cfg.MaxBudgetUSD = &v
	}
	cfg.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	return cfg, nil
}

// UpdateSchedulerConfig upserts the single scheduler config row.
func (s *Store) UpdateSchedulerConfig(ctx context.Context, cfg SchedulerConfig) error {
	reservedJSON, err := json.Marshal(cfg.ReservedBlocks)
	if err != nil {
		return fmt.Errorf("encoding reserved_blocks: %w", err)
	}
	preferredJSON, err := json.Marshal(cfg.PreferredWindows)
	if err != nil {
		return fmt.Errorf("encoding preferred_windows: %w", err)
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scheduler_config (id, enabled, aggressiveness, reserved_blocks, preferred_windows, max_budget_usd, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			enabled = excluded.enabled,
			aggressiveness = excluded.aggressiveness,
			reserved_blocks = excluded.reserved_blocks,
			preferred_windows = excluded.preferred_windows,
			max_budget_usd = excluded.max_budget_usd,
			updated_at = excluded.updated_at
	`, boolToInt(cfg.Enabled), cfg.Aggressiveness, string(reservedJSON), string(preferredJSON), cfg.MaxBudgetUSD, now.Unix())
	if err != nil {
		return fmt.Errorf("upserting scheduler config: %w", err)
	}
	return nil
}

// UpdateMaxBudgetUSD sets just the global safety cap, leaving every other
// scheduler config field untouched (reading-modifying-writing the current
// row so a caller that only cares about this one field doesn't have to
// fetch the rest first).
func (s *Store) UpdateMaxBudgetUSD(ctx context.Context, maxBudgetUSD *float64) error {
	cfg, err := s.GetSchedulerConfig(ctx)
	if err != nil {
		return err
	}
	cfg.MaxBudgetUSD = maxBudgetUSD
	return s.UpdateSchedulerConfig(ctx, cfg)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
