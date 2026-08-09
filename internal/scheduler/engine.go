package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/dispatch"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/store"
)

// TickInterval is the dispatch loop's cadence (REQUIREMENTS.md §6.2).
const TickInterval = 30 * time.Second

// Engine runs the budget-aware dispatch loop on top of internal/dispatch,
// internal/queue, and internal/budget. See the package doc for what this
// slice does and does not implement.
type Engine struct {
	store    *store.Store
	queue    *queue.Queue
	dispatch *dispatch.Dispatcher
	ledger   *budget.Ledger
	clock    Clock
}

// New builds an Engine. clock defaults to RealClock{} if nil.
func New(s *store.Store, q *queue.Queue, d *dispatch.Dispatcher, l *budget.Ledger, clock Clock) *Engine {
	if clock == nil {
		clock = RealClock{}
	}
	return &Engine{store: s, queue: q, dispatch: d, ledger: l, clock: clock}
}

// TickResult reports what one Tick call did — used by tests and by Run's
// logging.
type TickResult struct {
	Decision    Decision
	Usage       UsageState
	Dispatched  bool
	JobID       string
	DispatchErr error
}

// Tick runs one iteration of REQUIREMENTS.md §6.2's dispatch loop: load
// config, refresh usage state (step 1), apply the admission checks (steps
// 2-4), and if nothing holds it back, dispatch the highest-priority
// runnable job (steps 7-8). It dispatches synchronously — Tick doesn't
// return until the job it started finishes. That's deliberate for this
// slice: REQUIREMENTS.md §10 open question 2 (concurrency) is
// unresolved, so nothing here runs more than one job at a time, matching
// the cold-start policy in §10 open question 3.
func (e *Engine) Tick(ctx context.Context) (TickResult, error) {
	cfg, err := e.store.GetSchedulerConfig(ctx)
	if err != nil {
		return TickResult{}, fmt.Errorf("getting scheduler config: %w", err)
	}

	now := e.clock.Now()
	usage, err := computeUsageState(ctx, e.ledger, now)
	if err != nil {
		return TickResult{}, fmt.Errorf("computing usage state: %w", err)
	}

	decision := Decide(cfg, usage, now)
	if decision.Action != ActionDispatch {
		return TickResult{Decision: decision, Usage: usage}, nil
	}

	job, ok, err := e.queue.NextRunnable(ctx, now)
	if err != nil {
		return TickResult{}, fmt.Errorf("finding next runnable job: %w", err)
	}
	if !ok {
		return TickResult{Decision: decision, Usage: usage}, nil
	}

	dispatchErr := e.dispatch.DispatchOne(ctx, job.ID)
	result := TickResult{Decision: decision, Usage: usage, Dispatched: true, JobID: job.ID, DispatchErr: dispatchErr}
	if dispatchErr != nil && !errors.Is(dispatchErr, dispatch.ErrHalted) {
		return result, fmt.Errorf("dispatching job %s: %w", job.ID, dispatchErr)
	}
	return result, nil
}

// Run calls Tick on TickInterval cadence until ctx is cancelled. A given
// tick's error is logged rather than fatal — a transient issue (a single
// failed dispatch, a DB hiccup) shouldn't stop a loop meant to run
// unattended for days (FR-SCHED-6).
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := e.Tick(ctx); err != nil {
				log.Printf("scheduler: tick error: %v", err)
			}
		}
	}
}
