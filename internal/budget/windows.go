package budget

import (
	"context"
	"fmt"
	"time"
)

// WindowTotals sums every usage entry ledger fell within a window. It is
// exact — it doesn't answer "how full is the plan's window," only "how
// much did tokenwarden itself dispatch in this span" (see package doc).
type WindowTotals struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	OutputTokens             int
	CostUSD                  float64
	EntryCount               int
}

const (
	fiveHourWindow = 5 * time.Hour
	sevenDayWindow = 7 * 24 * time.Hour
)

// FiveHourTotal sums ledger entries recorded in the 5 hours up to now.
func (l *Ledger) FiveHourTotal(ctx context.Context, now time.Time) (WindowTotals, error) {
	return l.windowTotal(ctx, now.Add(-fiveHourWindow))
}

// SevenDayTotal sums ledger entries recorded in the 7 days up to now.
func (l *Ledger) SevenDayTotal(ctx context.Context, now time.Time) (WindowTotals, error) {
	return l.windowTotal(ctx, now.Add(-sevenDayWindow))
}

func (l *Ledger) windowTotal(ctx context.Context, since time.Time) (WindowTotals, error) {
	entries, err := l.store.ListUsageSince(ctx, since)
	if err != nil {
		return WindowTotals{}, fmt.Errorf("summing usage since %s: %w", since, err)
	}

	var t WindowTotals
	for _, e := range entries {
		t.InputTokens += e.InputTokens
		t.CacheCreationInputTokens += e.CacheCreationInputTokens
		t.CacheReadInputTokens += e.CacheReadInputTokens
		t.OutputTokens += e.OutputTokens
		t.CostUSD += e.CostUSD
		t.EntryCount++
	}
	return t, nil
}
