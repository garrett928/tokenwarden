package scheduler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/dispatch"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

// fakeClaudeBin stands in for the real claude CLI (CLAUDE.md/NFR-TEST-1):
// the whole point of Engine.Tick calling into internal/dispatch for real
// is exercised with zero tokens spent and no network.
var fakeClaudeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeclaude-scheduler")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating temp dir for fakeclaude:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	out := filepath.Join(dir, "fakeclaude")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	build := exec.Command("go", "build", "-o", out, "../runner/testdata/fakeclaude")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building fakeclaude: %v\n%s", err, output)
		os.Exit(1)
	}
	fakeClaudeBin = out

	os.Exit(m.Run())
}

type testEngine struct {
	engine *Engine
	queue  *queue.Queue
	store  *store.Store
	clock  *SimClock
}

func newTestEngine(t *testing.T, start time.Time) testEngine {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	q := queue.New(s)
	r := runner.New(fakeClaudeBin)
	l := budget.New(s)
	d := dispatch.New(q, r, l)
	clock := NewSimClock(start)
	e := New(s, q, d, l, clock)

	return testEngine{engine: e, queue: q, store: s, clock: clock}
}

func minimalJob() store.Job {
	return store.Job{Kind: store.JobKindResearch, Prompt: "say pong"}
}

// TestTick_EmptyQueue_DispatchesNothing covers NFR-TEST-2's empty-queue
// scenario: an enabled scheduler with plenty of headroom but no jobs
// queued should report ActionDispatch (nothing held it back) without
// actually dispatching anything.
func TestTick_EmptyQueue_DispatchesNothing(t *testing.T) {
	te := newTestEngine(t, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()

	if err := te.store.UpdateSchedulerConfig(ctx, store.SchedulerConfig{Enabled: true, Aggressiveness: 80}); err != nil {
		t.Fatal(err)
	}

	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if got.Dispatched {
		t.Errorf("Dispatched = true with an empty queue, want false")
	}
	if got.Decision.Action != ActionDispatch {
		t.Errorf("Decision.Action = %v, want ActionDispatch (nothing held the tick back)", got.Decision.Action)
	}
}

// TestTick_Disabled_NeverDispatches covers the scheduler-off case: a
// queued job sits untouched.
func TestTick_Disabled_NeverDispatches(t *testing.T) {
	te := newTestEngine(t, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()

	if _, err := te.queue.Enqueue(ctx, minimalJob()); err != nil {
		t.Fatal(err)
	}
	// Default config is disabled.

	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if got.Dispatched {
		t.Error("Dispatched = true while scheduler disabled, want false")
	}
	if got.Decision.Action != ActionDisabled {
		t.Errorf("Decision.Action = %v, want ActionDisabled", got.Decision.Action)
	}
}

// TestTick_OversubscribedQueue_DispatchesHighestPriority covers
// NFR-TEST-2's oversubscribed-queue scenario: multiple runnable jobs,
// exactly one dispatched per tick, highest priority first.
func TestTick_OversubscribedQueue_DispatchesHighestPriority(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	te := newTestEngine(t, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()

	if err := te.store.UpdateSchedulerConfig(ctx, store.SchedulerConfig{Enabled: true, Aggressiveness: 80}); err != nil {
		t.Fatal(err)
	}

	low := minimalJob()
	low.Priority = 1
	lowJob, err := te.queue.Enqueue(ctx, low)
	if err != nil {
		t.Fatal(err)
	}
	high := minimalJob()
	high.Priority = 10
	highJob, err := te.queue.Enqueue(ctx, high)
	if err != nil {
		t.Fatal(err)
	}

	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if !got.Dispatched || got.JobID != highJob.ID {
		t.Fatalf("Tick() dispatched job %q (dispatched=%v), want the high-priority job %q", got.JobID, got.Dispatched, highJob.ID)
	}

	dispatched, err := te.queue.Get(ctx, highJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.Status != store.StatusSucceeded {
		t.Errorf("high-priority job Status = %q, want succeeded", dispatched.Status)
	}

	untouched, err := te.queue.Get(ctx, lowJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Status != store.StatusQueued {
		t.Errorf("low-priority job Status = %q, want still queued (only one job per tick)", untouched.Status)
	}
}

// TestTick_BrokenBlockedJob_DoesNotWedgeTheTick covers the failure mode
// where one unfixable blocked job (here, a dangling DependsOn) makes
// PromoteReady return an error every sweep: since PromoteReady already
// collects per-job errors rather than aborting, Tick must log and carry on
// to dispatch the jobs that are fine — otherwise the whole scheduler stalls
// forever on one bad row.
func TestTick_BrokenBlockedJob_DoesNotWedgeTheTick(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	te := newTestEngine(t, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()

	if err := te.store.UpdateSchedulerConfig(ctx, store.SchedulerConfig{Enabled: true, Aggressiveness: 80}); err != nil {
		t.Fatal(err)
	}

	// Created through the store directly: Enqueue would (correctly) reject a
	// dependency that doesn't exist, but a row like this can survive a
	// deleted dependency.
	broken := minimalJob()
	broken.Status = store.StatusBlocked
	broken.DependsOn = []string{"job_deleted_out_from_under_it"}
	if _, err := te.store.CreateJob(ctx, broken); err != nil {
		t.Fatal(err)
	}

	healthy, err := te.queue.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}

	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v — a broken blocked job must not fail the whole tick", err)
	}
	if !got.Dispatched || got.JobID != healthy.ID {
		t.Fatalf("Tick() dispatched job %q (dispatched=%v), want the healthy job %q", got.JobID, got.Dispatched, healthy.ID)
	}
}

// TestTick_ReservedBlock_HoldsOff covers NFR-TEST-2's reserved-block
// scenario.
func TestTick_ReservedBlock_HoldsOff(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	noon := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) // Monday
	te := newTestEngine(t, noon)
	ctx := context.Background()

	cfg := store.SchedulerConfig{
		Enabled:        true,
		Aggressiveness: 80,
		ReservedBlocks: []store.TimeBlock{{StartMin: 9 * 60, EndMin: 17 * 60}},
	}
	if err := te.store.UpdateSchedulerConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	job, err := te.queue.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}

	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if got.Dispatched {
		t.Error("Dispatched = true inside a reserved block, want false")
	}
	if got.Decision.Action != ActionReservedBlock {
		t.Errorf("Decision.Action = %v, want ActionReservedBlock", got.Decision.Action)
	}

	still, err := te.queue.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != store.StatusQueued {
		t.Errorf("job Status = %q, want still queued", still.Status)
	}

	// Advance past the reserved block: now dispatch should proceed.
	te.clock.Set(time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC))
	got, err = te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() after reserved block error: %v", err)
	}
	if !got.Dispatched {
		t.Error("Dispatched = false after leaving the reserved block, want true")
	}
}

// seedFittingData records enough history for both five-hour calibration
// and per-kind cost prediction to clear their cold-start thresholds
// (minCalibrationSamples/minCostSamples, both 3): four ground-truth
// readings sharing one FiveHourResetsAt, climbing 60/70/74/76%, each pair
// exactly 100 tokens-per-percent apart in the ledger (-> TokensPerPercent =
// 100, Samples = 3); and three completed research-kind jobs averaging 500
// tokens / $0.10 each (-> a research job's predicted cost is ~5% of the
// five-hour window and ~$0.0002/token). The filler usage entries backing
// calibration are timestamped well before the reading window so they don't
// also get counted as part of a warm-up job's own total.
//
// After this, at the returned "now" (just after the last reading, so
// ground truth is still fresh — see groundTruthFreshness), the five-hour
// window reads 76% used against a 78% ceiling (80% aggressiveness): only
// 2% headroom remains, comfortably less than a research job's predicted
// 5%, so a plain (non-Resumable) research candidate is oversized.
func seedFittingData(t *testing.T, te testEngine, start time.Time) time.Time {
	t.Helper()
	ctx := context.Background()
	ledger := budget.New(te.store)
	resetsAt := start.Add(5 * time.Hour)
	sevenDayResetsAt := start.Add(3 * 24 * time.Hour)
	filler := start.Add(-48 * time.Hour)

	pcts := []int{60, 70, 74, 76}
	var last time.Time
	for i, pct := range pcts {
		ts := start.Add(time.Duration(i) * time.Hour)
		last = ts
		if err := ledger.RecordGroundTruth(ctx, budget.GroundTruthReading{
			FiveHourUsedPercentage: pct,
			FiveHourResetsAt:       resetsAt,
			SevenDayUsedPercentage: pct,
			SevenDayResetsAt:       sevenDayResetsAt,
			ObservedAt:             ts,
		}); err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			deltaPct := pct - pcts[i-1]
			if _, err := te.store.RecordUsage(ctx, store.UsageEntry{
				JobID:        "calibration-filler",
				InputTokens:  deltaPct * 100, // 100 tokens-per-percent
				RecordedAt:   ts.Add(-time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	for i := 0; i < 3; i++ {
		j, err := te.store.CreateJob(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "warm up"})
		if err != nil {
			t.Fatal(err)
		}
		if err := te.store.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := te.store.RecordUsage(ctx, store.UsageEntry{JobID: j.ID, InputTokens: 500, CostUSD: 0.1, RecordedAt: filler}); err != nil {
			t.Fatal(err)
		}
	}

	return last
}

// TestTick_OversizedJob_DefersAndDispatchesNextBestCandidate covers §6.4
// strategy 4 plus §6.2 step 7's "try the next-best job": the top-priority
// candidate's predicted cost doesn't fit remaining five-hour headroom and
// it isn't Resumable, so no fitting strategy applies — it's deferred, and
// the engine moves on to try the next candidate in the same tick.
func TestTick_OversizedJob_DefersAndDispatchesNextBestCandidate(t *testing.T) {
	te := newTestEngine(t, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	start := te.clock.Now()

	if err := te.store.UpdateSchedulerConfig(ctx, store.SchedulerConfig{Enabled: true, Aggressiveness: 80}); err != nil {
		t.Fatal(err)
	}
	last := seedFittingData(t, te, start)
	te.clock.Set(last.Add(time.Minute)) // ground truth still fresh

	big := store.Job{Kind: store.JobKindResearch, Prompt: "oversized", Priority: 10}
	bigJob, err := te.queue.Enqueue(ctx, big)
	if err != nil {
		t.Fatal(err)
	}
	// A plan-kind job has no prediction history yet (cold start,
	// Insufficient) -> FitJob dispatches it without trying to fit it.
	small := store.Job{Kind: store.JobKindPlan, Prompt: "small", Priority: 1}
	smallJob, err := te.queue.Enqueue(ctx, small)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if !got.Dispatched || got.JobID != smallJob.ID {
		t.Fatalf("Tick() dispatched job %q (dispatched=%v), want the next-best candidate %q", got.JobID, got.Dispatched, smallJob.ID)
	}
	if len(got.DeferredJobIDs) != 1 || got.DeferredJobIDs[0] != bigJob.ID {
		t.Errorf("DeferredJobIDs = %v, want [%s]", got.DeferredJobIDs, bigJob.ID)
	}

	deferred, err := te.queue.Get(ctx, bigJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deferred.Status != store.StatusDeferredOversized {
		t.Errorf("oversized job Status = %q, want %q", deferred.Status, store.StatusDeferredOversized)
	}
	if deferred.FailureReason == "" {
		t.Error("oversized job FailureReason is empty, want the deferral reason recorded")
	}

	dispatched, err := te.queue.Get(ctx, smallJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.Status != store.StatusSucceeded {
		t.Errorf("next-best job Status = %q, want succeeded", dispatched.Status)
	}
}

// TestTick_OversizedResumableJob_CapsBudgetAndDispatches covers §6.4
// strategy 1: a top-priority candidate that doesn't fit remaining headroom
// but is Resumable gets its budget capped to that headroom and dispatched
// anyway, rather than deferred.
func TestTick_OversizedResumableJob_CapsBudgetAndDispatches(t *testing.T) {
	te := newTestEngine(t, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	start := te.clock.Now()

	if err := te.store.UpdateSchedulerConfig(ctx, store.SchedulerConfig{Enabled: true, Aggressiveness: 80}); err != nil {
		t.Fatal(err)
	}
	last := seedFittingData(t, te, start)
	te.clock.Set(last.Add(time.Minute))

	job := store.Job{Kind: store.JobKindResearch, Prompt: "oversized but resumable", Priority: 10, Resumable: true}
	created, err := te.queue.Enqueue(ctx, job)
	if err != nil {
		t.Fatal(err)
	}

	// The fixture's total_cost_usd (0.05) exceeds the ~$0.04 cap FitJob
	// should compute (2% remaining headroom * 100 tokens/% * $0.0002/token),
	// so the dispatch itself will register as a budget cutoff (see
	// dispatch.isBudgetCutoff) once capped.
	t.Setenv("FAKECLAUDE_FIXTURE", "budget_capped")
	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if !got.Dispatched || got.JobID != created.ID {
		t.Fatalf("Tick() dispatched job %q (dispatched=%v), want the capped job %q", got.JobID, got.Dispatched, created.ID)
	}
	if len(got.DeferredJobIDs) != 0 {
		t.Errorf("DeferredJobIDs = %v, want none (the only candidate was capped, not deferred)", got.DeferredJobIDs)
	}
	if got.BudgetCapUSD <= 0 || got.BudgetCapUSD >= 0.05 {
		t.Errorf("BudgetCapUSD = %v, want a positive cap below the fixture's 0.05 cost", got.BudgetCapUSD)
	}

	after, err := te.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != store.StatusPausedBudget {
		t.Errorf("Status = %q, want %q (capped run hit its cap)", after.Status, store.StatusPausedBudget)
	}
	if after.MaxBudgetUSD == nil || *after.MaxBudgetUSD != got.BudgetCapUSD {
		t.Errorf("MaxBudgetUSD = %v, want %v (the cap FitJob computed)", after.MaxBudgetUSD, got.BudgetCapUSD)
	}
	if after.SessionID == "" {
		t.Error("SessionID is empty, want it recorded so a later tick can --resume")
	}
}

// stepChildren returns every job promoted from parentID, in creation
// order — the chain PromoteSteps built, as the store actually holds it.
func stepChildren(t *testing.T, te testEngine, parentID string) []store.Job {
	t.Helper()
	children, err := te.queue.List(context.Background(), store.ListFilter{ParentJobID: parentID})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	// Ordered by the DependsOn links, not CreatedAt: a promotion's children
	// are all created within the same one-second created_at tick, so
	// timestamps can't tell the chain's head from its tail.
	successor := make(map[string]store.Job, len(children))
	var head store.Job
	for _, c := range children {
		if len(c.DependsOn) == 0 {
			head = c
			continue
		}
		successor[c.DependsOn[0]] = c
	}
	ordered := make([]store.Job, 0, len(children))
	for cur := head; ; {
		ordered = append(ordered, cur)
		next, ok := successor[cur.ID]
		if !ok || len(ordered) == len(children) {
			break
		}
		cur = next
	}
	if len(ordered) != len(children) {
		t.Fatalf("children of %s do not form one dependency chain: %+v", parentID, children)
	}
	return ordered
}

// TestTick_OversizedJobWithSteps_PromotesAndDispatchesNextBestCandidate
// covers §6.4 strategy 2 end to end: the top-priority candidate's
// predicted cost doesn't fit remaining five-hour headroom and it isn't
// Resumable (so strategy 1 doesn't apply), but it declares Steps — so
// instead of being parked as oversized (strategy 4), it's promoted into a
// chain of child jobs. Nothing from that chain runs this tick: the
// children didn't exist when candidates were fetched, so the engine moves
// on to the next-best candidate exactly as it does after a defer.
func TestTick_OversizedJobWithSteps_PromotesAndDispatchesNextBestCandidate(t *testing.T) {
	te := newTestEngine(t, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	start := te.clock.Now()

	if err := te.store.UpdateSchedulerConfig(ctx, store.SchedulerConfig{Enabled: true, Aggressiveness: 80}); err != nil {
		t.Fatal(err)
	}
	last := seedFittingData(t, te, start)
	te.clock.Set(last.Add(time.Minute)) // ground truth still fresh

	steps := []string{"survey the modules", "draft the migration", "write the tests"}
	big := store.Job{
		Kind:      store.JobKindResearch,
		Prompt:    "oversized, but the user declared split points",
		Priority:  10,
		Resumable: false,
		Steps:     steps,
	}
	bigJob, err := te.queue.Enqueue(ctx, big)
	if err != nil {
		t.Fatal(err)
	}
	// A plan-kind job has no prediction history yet (cold start,
	// Insufficient) -> FitJob dispatches it without trying to fit it.
	small := store.Job{Kind: store.JobKindPlan, Prompt: "small", Priority: 1}
	smallJob, err := te.queue.Enqueue(ctx, small)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}

	if len(got.PromotedJobIDs) != 1 || got.PromotedJobIDs[0] != bigJob.ID {
		t.Errorf("PromotedJobIDs = %v, want [%s]", got.PromotedJobIDs, bigJob.ID)
	}
	if len(got.DeferredJobIDs) != 0 {
		t.Errorf("DeferredJobIDs = %v, want none (declared steps should be promoted, not deferred)", got.DeferredJobIDs)
	}
	if !got.Dispatched || got.JobID != smallJob.ID {
		t.Fatalf("Tick() dispatched job %q (dispatched=%v), want the next-best candidate %q", got.JobID, got.Dispatched, smallJob.ID)
	}

	promoted, err := te.queue.Get(ctx, bigJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != store.StatusPromoted {
		t.Errorf("oversized job Status = %q, want %q", promoted.Status, store.StatusPromoted)
	}
	if promoted.FailureReason == "" {
		t.Error("promoted job FailureReason is empty, want it naming the children it became")
	}

	children := stepChildren(t, te, bigJob.ID)
	if len(children) != len(steps) {
		t.Fatalf("found %d child jobs for the promoted parent, want one per step (%d)", len(children), len(steps))
	}
	for i, child := range children {
		if child.Prompt != steps[i] {
			t.Errorf("child %d Prompt = %q, want step %q", i, child.Prompt, steps[i])
		}
		wantStatus := store.StatusBlocked
		if i == 0 {
			wantStatus = store.StatusQueued
		}
		if child.Status != wantStatus {
			t.Errorf("child %d Status = %q, want %q — promotion queues the chain, it doesn't dispatch from it this tick", i, child.Status, wantStatus)
		}
	}

	dispatched, err := te.queue.Get(ctx, smallJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.Status != store.StatusSucceeded {
		t.Errorf("next-best job Status = %q, want succeeded", dispatched.Status)
	}
}

// TestTick_PromotedStepChain_AdvancesWithInheritedSession follows a
// promoted chain across several ticks: promote, run step one, advance to
// step two, run step two. The point of interest is the handoff — step two
// must pick up step one's session ID so its dispatch is a --resume of the
// same conversation rather than a cold start that has forgotten what step
// one did. A reserved block on the advancing tick also pins down why
// PromoteReady runs before Tick's admission checks rather than after: the
// chain has to keep advancing even on a tick that will dispatch nothing.
func TestTick_PromotedStepChain_AdvancesWithInheritedSession(t *testing.T) {
	te := newTestEngine(t, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	start := te.clock.Now()

	openCfg := store.SchedulerConfig{Enabled: true, Aggressiveness: 80}
	if err := te.store.UpdateSchedulerConfig(ctx, openCfg); err != nil {
		t.Fatal(err)
	}
	last := seedFittingData(t, te, start)
	te.clock.Set(last.Add(time.Minute))

	parent := store.Job{
		Kind:      store.JobKindResearch,
		Prompt:    "oversized, two declared steps",
		Priority:  10,
		Resumable: false,
		Steps:     []string{"gather the sources", "summarize them"},
	}
	parentJob, err := te.queue.Enqueue(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}

	// Tick 1: promote. It's the only candidate, so nothing dispatches.
	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() 1 error: %v", err)
	}
	if got.Dispatched {
		t.Errorf("Tick 1 dispatched job %q, want nothing (the promoted children aren't candidates until the next tick)", got.JobID)
	}
	if len(got.PromotedJobIDs) != 1 || got.PromotedJobIDs[0] != parentJob.ID {
		t.Fatalf("Tick 1 PromotedJobIDs = %v, want [%s]", got.PromotedJobIDs, parentJob.ID)
	}
	children := stepChildren(t, te, parentJob.ID)
	if len(children) != 2 {
		t.Fatalf("found %d child jobs, want 2", len(children))
	}

	// The five-hour window rolls over between ticks, so a single step now
	// fits remaining headroom on its own. It has to: children inherit the
	// parent's Resumable=false, so a step that were still oversized would
	// defer (strategy 4) rather than be budget-capped (strategy 1) —
	// promotion must not hand a step a weaker interruptibility guarantee
	// than the user set on the whole job. The reading carries a new
	// FiveHourResetsAt, so calibration ignores the pair (a rollover isn't
	// spend) and the fit maths stays the one seedFittingData set up.
	rollover := te.clock.Now().Add(time.Minute)
	if err := budget.New(te.store).RecordGroundTruth(ctx, budget.GroundTruthReading{
		FiveHourUsedPercentage: 10,
		FiveHourResetsAt:       rollover.Add(5 * time.Hour),
		SevenDayUsedPercentage: 76,
		SevenDayResetsAt:       start.Add(3 * 24 * time.Hour),
		ObservedAt:             rollover,
	}); err != nil {
		t.Fatal(err)
	}

	// Tick 2: the chain's head runs, dispatched as-is now that it fits. The
	// rate_limit fixture reports session rl-session-001, which is what the
	// next step has to inherit.
	t.Setenv("FAKECLAUDE_FIXTURE", "rate_limit")
	te.clock.Advance(time.Minute)
	got, err = te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() 2 error: %v", err)
	}
	if !got.Dispatched || got.JobID != children[0].ID {
		t.Fatalf("Tick 2 dispatched job %q (dispatched=%v), want the first step child %q", got.JobID, got.Dispatched, children[0].ID)
	}
	firstStep, err := te.queue.Get(ctx, children[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstStep.Status != store.StatusSucceeded {
		t.Fatalf("first step Status = %q, want succeeded (fixture cost $0.01 is under the computed cap %v)", firstStep.Status, got.BudgetCapUSD)
	}
	if firstStep.SessionID != "rl-session-001" {
		t.Fatalf("first step SessionID = %q, want the fixture's %q", firstStep.SessionID, "rl-session-001")
	}
	secondStep, err := te.queue.Get(ctx, children[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondStep.Status != store.StatusBlocked {
		t.Errorf("second step Status = %q, want still %q — PromoteReady runs at the top of a tick, so the chain advances on the next one", secondStep.Status, store.StatusBlocked)
	}

	// Tick 3: inside a reserved block, so the admission check returns early
	// and nothing dispatches — but the chain must still advance, and step
	// two must come out of it carrying step one's session.
	blockedCfg := openCfg
	blockedCfg.ReservedBlocks = []store.TimeBlock{{StartMin: 0, EndMin: 5 * 60}} // the simulated clock sits at ~03:0x UTC
	if err := te.store.UpdateSchedulerConfig(ctx, blockedCfg); err != nil {
		t.Fatal(err)
	}
	te.clock.Advance(time.Minute)
	got, err = te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() 3 error: %v", err)
	}
	if got.Decision.Action != ActionReservedBlock {
		t.Fatalf("Tick 3 Decision.Action = %v, want ActionReservedBlock", got.Decision.Action)
	}
	if got.Dispatched {
		t.Errorf("Tick 3 dispatched job %q inside a reserved block, want nothing", got.JobID)
	}

	secondStep, err = te.queue.Get(ctx, children[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondStep.Status != store.StatusQueued {
		t.Errorf("second step Status = %q, want %q — dependency bookkeeping runs even on a tick that dispatches nothing", secondStep.Status, store.StatusQueued)
	}
	if secondStep.SessionID != firstStep.SessionID {
		t.Fatalf("second step SessionID = %q, want %q inherited from the step that just succeeded", secondStep.SessionID, firstStep.SessionID)
	}
	// The inherited session is only useful if it actually reaches the CLI
	// invocation as a --resume of the same conversation.
	args, err := runner.BuildArgs(secondStep)
	if err != nil {
		t.Fatalf("BuildArgs() error: %v", err)
	}
	if !argvHasFlagValue(args, "--resume", firstStep.SessionID) {
		t.Errorf("BuildArgs(second step) = %v, want --resume %s (the inherited session)", args, firstStep.SessionID)
	}

	// Tick 4: reserved block over — step two dispatches.
	if err := te.store.UpdateSchedulerConfig(ctx, openCfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	te.clock.Advance(time.Minute)
	got, err = te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() 4 error: %v", err)
	}
	if !got.Dispatched || got.JobID != children[1].ID {
		t.Fatalf("Tick 4 dispatched job %q (dispatched=%v), want the second step child %q", got.JobID, got.Dispatched, children[1].ID)
	}
	final, err := te.queue.Get(ctx, children[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != store.StatusSucceeded {
		t.Errorf("second step Status = %q, want succeeded", final.Status)
	}
}

// argvHasFlagValue reports whether args contains flag immediately followed
// by value.
func argvHasFlagValue(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// TestTick_FiveHourCeilingReached_HoldsOffUntilInjectedRateLimit covers
// NFR-TEST-2's "injected rate-limit errors" scenario at the policy layer:
// once ground truth reports the window at the aggressiveness ceiling, the
// engine holds off and reports the reset time to sleep until, rather than
// dispatching past it.
func TestTick_FiveHourCeilingReached_HoldsOff(t *testing.T) {
	te := newTestEngine(t, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()

	if err := te.store.UpdateSchedulerConfig(ctx, store.SchedulerConfig{Enabled: true, Aggressiveness: 80}); err != nil {
		t.Fatal(err)
	}
	if _, err := te.queue.Enqueue(ctx, minimalJob()); err != nil {
		t.Fatal(err)
	}

	resetsAt := te.clock.Now().Add(90 * time.Minute)
	ledger := budget.New(te.store)
	if err := ledger.RecordGroundTruth(ctx, budget.GroundTruthReading{
		FiveHourUsedPercentage: 79, // within 2% safety margin of 80% aggressiveness
		FiveHourResetsAt:       resetsAt,
		SevenDayUsedPercentage: 5,
		SevenDayResetsAt:       te.clock.Now().Add(3 * 24 * time.Hour),
		ObservedAt:             te.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if got.Dispatched {
		t.Error("Dispatched = true at the aggressiveness ceiling, want false")
	}
	if got.Decision.Action != ActionFiveHourCeiling {
		t.Fatalf("Decision.Action = %v, want ActionFiveHourCeiling", got.Decision.Action)
	}
	if got.Decision.SleepUntil.Unix() != resetsAt.Unix() {
		t.Errorf("SleepUntil = %v, want %v", got.Decision.SleepUntil, resetsAt)
	}

	// Advance the simulated clock to the reset time with a fresh reading
	// showing the window emptied out; dispatch should resume.
	te.clock.Set(resetsAt.Add(time.Minute))
	if err := ledger.RecordGroundTruth(ctx, budget.GroundTruthReading{
		FiveHourUsedPercentage: 0,
		FiveHourResetsAt:       resetsAt.Add(5 * time.Hour),
		SevenDayUsedPercentage: 5,
		SevenDayResetsAt:       te.clock.Now().Add(3 * 24 * time.Hour),
		ObservedAt:             te.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")

	got, err = te.engine.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() after reset error: %v", err)
	}
	if !got.Dispatched {
		t.Error("Dispatched = false after the window reset, want true")
	}
}
