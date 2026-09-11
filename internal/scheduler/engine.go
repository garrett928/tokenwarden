package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
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
	Decision   Decision
	Usage      UsageState
	Dispatched bool
	// JobID and DispatchErr describe the job actually dispatched this
	// tick, if any — the last candidate FitJob approved (as-is or capped),
	// not necessarily Candidates' first entry, since earlier ones may have
	// been deferred (see DeferredJobIDs).
	JobID        string
	DispatchErr  error
	BudgetCapUSD float64 // set when JobID was dispatched via FitCapBudget (§6.4 strategy 1)
	// DeferredJobIDs lists candidates this tick deferred as oversized
	// (§6.4 strategy 4) before finding one that fit — empty on a tick
	// where the first candidate dispatched cleanly.
	DeferredJobIDs []string
	// PromotedJobIDs lists candidates this tick promoted into step children
	// (§6.4 strategy 2) before finding one that fit. Like a defer, a
	// promotion dispatches nothing itself: the children are queued for a
	// later tick to pick up.
	PromotedJobIDs []string
	// PromotedChildJobIDs lists the child jobs those promotions produced,
	// in chain order, flattened across every promotion this tick — so an
	// operator (and a test) can see what a promotion actually created
	// rather than only that one happened.
	PromotedChildJobIDs []string
}

// Tick runs one iteration of REQUIREMENTS.md §6.2's dispatch loop: load
// config, refresh usage state (step 1), apply the admission checks (steps
// 2-4), and if nothing holds it back, walk runnable candidates in priority
// order (step 7) fitting each against remaining five-hour headroom (§6.4)
// until one dispatches (step 8) or all of them defer or promote. It dispatches
// synchronously — Tick doesn't return until the job it started finishes.
// That's deliberate for this slice: REQUIREMENTS.md §10 open question 2
// (concurrency) is unresolved, so nothing here runs more than one job at a
// time.
func (e *Engine) Tick(ctx context.Context) (TickResult, error) {
	cfg, err := e.store.GetSchedulerConfig(ctx)
	if err != nil {
		return TickResult{}, fmt.Errorf("getting scheduler config: %w", err)
	}

	// Dependency bookkeeping, not dispatch pacing: a Blocked job whose
	// dependencies have since finished must become runnable (or cancelled)
	// whether or not this tick goes on to dispatch anything, so this runs
	// before the admission checks can return early. It's also what advances
	// a promoted step chain (§6.4 strategy 2) from one child to the next.
	//
	// PromoteReady already collects per-job errors instead of aborting its
	// own sweep, so an error here means some individual job is stuck (e.g. a
	// dangling dependency), not that the sweep failed. Logging and carrying
	// on is deliberate: returning would let one permanently-broken blocked
	// job wedge the whole scheduler, since every later tick would fail at
	// this same point before ever reaching Decide.
	if _, err := e.queue.PromoteReady(ctx); err != nil {
		log.Printf("scheduler: promoting ready jobs: %v", err)
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

	candidates, err := e.queue.Candidates(ctx, now)
	if err != nil {
		return TickResult{}, fmt.Errorf("finding runnable candidates: %w", err)
	}
	result := TickResult{Decision: decision, Usage: usage}
	if len(candidates) == 0 {
		return result, nil
	}

	// §10 open question 3: the fitting check only ever applies once
	// five-hour calibration itself is trustworthy. Fetched once per tick —
	// it doesn't change while candidates are being walked.
	fiveHourCal, err := e.ledger.CalibrateFiveHour(ctx)
	if err != nil {
		return TickResult{}, fmt.Errorf("calibrating five-hour window: %w", err)
	}

	predictions := make(map[store.JobKind]budget.CostPrediction)
	for _, job := range candidates {
		prediction, ok := predictions[job.Kind]
		if !ok {
			prediction, err = e.ledger.PredictJobCost(ctx, job.Kind)
			if err != nil {
				return TickResult{}, fmt.Errorf("predicting cost for job %s (kind %s): %w", job.ID, job.Kind, err)
			}
			predictions[job.Kind] = prediction
		}

		switch fit := FitJob(job, prediction, fiveHourCal, cfg, usage.FiveHourUsedPercent); fit.Action {
		case FitDefer:
			if err := e.queue.DeferOversized(ctx, job.ID, fit.Reason); err != nil {
				return TickResult{}, fmt.Errorf("deferring oversized job %s: %w", job.ID, err)
			}
			result.DeferredJobIDs = append(result.DeferredJobIDs, job.ID)
			continue // try the next-best candidate, §6.2 step 7
		case FitPromoteSteps:
			children, err := e.queue.PromoteSteps(ctx, job.ID)
			if err != nil {
				return TickResult{}, fmt.Errorf("promoting steps for job %s: %w", job.ID, err)
			}
			childIDs := make([]string, 0, len(children))
			for _, c := range children {
				childIDs = append(childIDs, c.ID)
			}
			log.Printf("scheduler: promoted job %s into %d step child job(s): %s", job.ID, len(childIDs), strings.Join(childIDs, ", "))
			result.PromotedJobIDs = append(result.PromotedJobIDs, job.ID)
			result.PromotedChildJobIDs = append(result.PromotedChildJobIDs, childIDs...)
			// The new children weren't in this tick's candidates, so they
			// wait for the next one; try the next-best candidate meanwhile.
			continue
		case FitCapBudget:
			if err := e.queue.CapBudget(ctx, job.ID, fit.BudgetCapUSD); err != nil {
				return TickResult{}, fmt.Errorf("capping budget for job %s: %w", job.ID, err)
			}
			result.BudgetCapUSD = fit.BudgetCapUSD
		}

		dispatchErr := e.dispatch.DispatchOne(ctx, job.ID)
		result.Dispatched = true
		result.JobID = job.ID
		result.DispatchErr = dispatchErr
		if dispatchErr != nil && !errors.Is(dispatchErr, dispatch.ErrHalted) {
			return result, fmt.Errorf("dispatching job %s: %w", job.ID, dispatchErr)
		}
		return result, nil
	}

	// Every candidate this tick was oversized: deferred, or promoted into
	// children a later tick will pick up.
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
