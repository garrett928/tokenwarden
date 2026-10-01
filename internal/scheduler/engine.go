package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
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

	// Counters for the heartbeat line, updated by Run's tick loop and read
	// by its heartbeat goroutine.
	ticks, dispatched, tickErrors atomic.Int64
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
	// Candidates is how many runnable jobs the tick saw (0 when an
	// admission check held the tick back before looking).
	Candidates int
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
	result := TickResult{Decision: decision, Usage: usage, Candidates: len(candidates)}
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
			if job.Status != store.StatusDeferredOversized || job.FailureReason != fit.Reason {
				slog.Info("job deferred as oversized", "job_id", job.ID, "kind", job.Kind, "reason", fit.Reason)
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
			slog.Info("job promoted into step children", "job_id", job.ID, "children", strings.Join(childIDs, ","))
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
			slog.Info("job budget capped to fit headroom", "job_id", job.ID, "kind", job.Kind, "cap_usd", fit.BudgetCapUSD)
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

// heartbeatInterval is how often Run logs a heartbeat line: a periodic
// proof of life with queue counts, so a quiet stretch of the log can be told
// apart from a hung or dead daemon.
const heartbeatInterval = 10 * time.Minute

// Run calls Tick on TickInterval cadence until ctx is cancelled. A given
// tick's error is logged rather than fatal — a transient issue (a single
// failed dispatch, a DB hiccup) shouldn't stop a loop meant to run
// unattended for days (FR-SCHED-6). Every tick logs one line (see logTick)
// and a heartbeat line goes out every heartbeatInterval.
func (e *Engine) Run(ctx context.Context) {
	if cfg, err := e.store.GetSchedulerConfig(ctx); err == nil {
		slog.Info("scheduler loop started", append([]any{"tick_interval", TickInterval.String()}, configAttrs(cfg)...)...)
	} else {
		slog.Error("scheduler loop started; reading config failed", "err", err)
	}
	// The heartbeat runs on its own goroutine: Tick dispatches
	// synchronously, so a multi-hour job would otherwise silence it — and a
	// silent log can't be told apart from a hung daemon.
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		hb := time.NewTicker(heartbeatInterval)
		defer hb.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-hb.C:
				e.logHeartbeat(ctx)
			}
		}
	}()

	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			<-hbDone
			slog.Info("scheduler loop stopped", "ticks", e.ticks.Load(), "dispatched", e.dispatched.Load(), "tick_errors", e.tickErrors.Load())
			return
		case <-ticker.C:
			res, err := e.Tick(ctx)
			e.ticks.Add(1)
			if res.Dispatched {
				e.dispatched.Add(1)
			}
			if err != nil {
				e.tickErrors.Add(1)
			}
			logTick(res, err)
		}
	}
}

// logTick writes the one line per tick that makes a run auditable: what the
// admission checks decided and why, what usage state they saw, and what (if
// anything) was dispatched, deferred, promoted, or capped.
func logTick(res TickResult, err error) {
	attrs := []any{
		"decision", res.Decision.Action.String(),
		"reason", res.Decision.Reason,
		"usage_source", string(res.Usage.Source),
		"five_hour_pct", res.Usage.FiveHourUsedPercent,
		"seven_day_pct", res.Usage.SevenDayUsedPercent,
		"candidates", res.Candidates,
		"dispatched", res.Dispatched,
	}
	if !res.Decision.SleepUntil.IsZero() {
		attrs = append(attrs, "sleep_until", res.Decision.SleepUntil.Format(time.RFC3339))
	}
	if res.JobID != "" {
		attrs = append(attrs, "job_id", res.JobID)
	}
	if res.BudgetCapUSD != 0 {
		attrs = append(attrs, "budget_cap_usd", res.BudgetCapUSD)
	}
	if len(res.DeferredJobIDs) > 0 {
		attrs = append(attrs, "deferred", strings.Join(res.DeferredJobIDs, ","))
	}
	if len(res.PromotedJobIDs) > 0 {
		attrs = append(attrs, "promoted", strings.Join(res.PromotedJobIDs, ","))
	}
	if err != nil {
		attrs = append(attrs, "err", err)
		slog.Error("scheduler tick", attrs...)
		return
	}
	slog.Info("scheduler tick", attrs...)
}

func (e *Engine) logHeartbeat(ctx context.Context) {
	attrs := []any{
		"ticks_total", e.ticks.Load(),
		"dispatched_total", e.dispatched.Load(),
		"tick_errors_total", e.tickErrors.Load(),
		"halted", e.dispatch.Halted(),
	}
	if cfg, err := e.store.GetSchedulerConfig(ctx); err == nil {
		attrs = append(attrs, "scheduler_enabled", cfg.Enabled)
	}
	if jobs, err := e.store.ListJobs(ctx, store.ListFilter{}); err == nil {
		counts := map[store.Status]int{}
		for _, j := range jobs {
			counts[j.Status]++
		}
		statuses := make([]string, 0, len(counts))
		for st := range counts {
			statuses = append(statuses, string(st))
		}
		sort.Strings(statuses)
		for _, st := range statuses {
			attrs = append(attrs, "jobs_"+st, counts[store.Status(st)])
		}
	} else {
		attrs = append(attrs, "jobs_err", err)
	}
	slog.Info("heartbeat", attrs...)
}

func configAttrs(cfg store.SchedulerConfig) []any {
	return []any{
		"enabled", cfg.Enabled,
		"aggressiveness", cfg.Aggressiveness,
		"reserved_blocks", len(cfg.ReservedBlocks),
		"preferred_windows", len(cfg.PreferredWindows),
	}
}
