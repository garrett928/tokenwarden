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
