package queue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tokenwarden/internal/store"
)

func newTestQueue(t *testing.T) (*Queue, *store.Store) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s), s
}

func minimalJob() store.Job {
	return store.Job{Kind: store.JobKindResearch, Prompt: "summarize the changelog"}
}

func TestEnqueue_NoDependencies_Queued(t *testing.T) {
	q, _ := newTestQueue(t)
	got, err := q.Enqueue(context.Background(), minimalJob())
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusQueued)
	}
}

func TestEnqueue_UnsatisfiedDependency_Blocked(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}

	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	got, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusBlocked {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusBlocked)
	}
}

func TestEnqueue_SatisfiedDependency_Queued(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, dep.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	got, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusQueued)
	}
}

func TestEnqueue_FailedDependency_CancelledWithReason(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, dep.ID, store.StatusFailed, "boom"); err != nil {
		t.Fatal(err)
	}

	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	got, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusCancelled {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusCancelled)
	}
	if got.FailureReason == "" {
		t.Error("FailureReason is empty, want an explanation naming the failed dependency")
	}
}

func TestEnqueue_MissingDependency_Error(t *testing.T) {
	q, _ := newTestQueue(t)
	j := minimalJob()
	j.DependsOn = []string{"job_does_not_exist"}
	if _, err := q.Enqueue(context.Background(), j); !errors.Is(err, ErrDependencyNotFound) {
		t.Errorf("Enqueue() error = %v, want ErrDependencyNotFound", err)
	}
}

func TestPromoteReady_PromotesWhenDependencySucceeds(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	blocked, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != store.StatusBlocked {
		t.Fatalf("precondition: Status = %q, want Blocked", blocked.Status)
	}

	// Nothing changed yet — sweep should be a no-op.
	n, err := q.PromoteReady(ctx)
	if err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	if n != 0 {
		t.Errorf("PromoteReady() before dep succeeds = %d, want 0", n)
	}

	if err := s.UpdateStatus(ctx, dep.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	n, err = q.PromoteReady(ctx)
	if err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	if n != 1 {
		t.Errorf("PromoteReady() after dep succeeds = %d, want 1", n)
	}

	got, err := q.Get(ctx, blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status after promotion = %q, want %q", got.Status, store.StatusQueued)
	}
}

func TestPromoteReady_CascadesCancellationWhenDependencyFails(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	blocked, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateStatus(ctx, dep.ID, store.StatusFailed, "boom"); err != nil {
		t.Fatal(err)
	}

	n, err := q.PromoteReady(ctx)
	if err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	if n != 1 {
		t.Errorf("PromoteReady() = %d, want 1", n)
	}

	got, err := q.Get(ctx, blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusCancelled {
		t.Errorf("Status = %q, want %q (dependency failed permanently)", got.Status, store.StatusCancelled)
	}
}

// TestPromoteReady_StepChildInheritsSessionIDFromDependency covers the
// other half of §6.4 strategy 2: a promoted chain shares one Claude
// session, so when a step child's single dependency succeeds carrying a
// SessionID, that ID is copied onto the child as it becomes Queued — which
// is what makes the next dispatch a --resume of the same conversation
// (runner.BuildArgs switches on SessionID being non-empty) rather than a
// cold start that has forgotten everything the previous step did.
func TestPromoteReady_StepChildInheritsSessionIDFromDependency(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	parent := minimalJob()
	parent.Steps = []string{"step one", "step two"}
	created, err := q.Enqueue(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	children, err := q.PromoteSteps(ctx, created.ID)
	if err != nil {
		t.Fatalf("PromoteSteps() error: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("PromoteSteps() returned %d children, want 2", len(children))
	}

	if _, err := q.MarkRunning(ctx, children[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Finish(ctx, children[0].ID, FinishOutcome{
		Status:    store.StatusSucceeded,
		SessionID: "sess-chain-001",
		Result:    "step one done",
	}); err != nil {
		t.Fatal(err)
	}

	n, err := q.PromoteReady(ctx)
	if err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	if n != 1 {
		t.Errorf("PromoteReady() = %d, want 1 (the second step child)", n)
	}

	got, err := q.Get(ctx, children[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("second step child Status = %q, want %q", got.Status, store.StatusQueued)
	}
	if got.SessionID != "sess-chain-001" {
		t.Errorf("second step child SessionID = %q, want %q inherited from the step that just succeeded", got.SessionID, "sess-chain-001")
	}
}

// TestPromoteReady_NonStepChildDoesNotInheritSessionID guards the blast
// radius of the inheritance rule above: an ordinary DependsOn relationship
// (no ParentJobID, i.e. not a promoted step chain) must keep starting its
// own session, since those two jobs are unrelated pieces of work that only
// happen to be ordered.
func TestPromoteReady_NonStepChildDoesNotInheritSessionID(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	blocked, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := q.MarkRunning(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Finish(ctx, dep.ID, FinishOutcome{Status: store.StatusSucceeded, SessionID: "sess-unrelated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.PromoteReady(ctx); err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}

	got, err := q.Get(ctx, blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Fatalf("Status = %q, want %q", got.Status, store.StatusQueued)
	}
	if got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty — only promoted step children (ParentJobID set) inherit a dependency's session", got.SessionID)
	}
}

// TestPromoteSteps_BuildsDependencyChain covers §6.4 strategy 2's core
// mechanic: Steps become one child job each, wired into a linear
// dependency chain so exactly one step is runnable at a time, with the
// parent retired to StatusPromoted.
func TestPromoteSteps_BuildsDependencyChain(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	parent := store.Job{
		Kind:      store.JobKindCode,
		Prompt:    "migrate the auth handler",
		Workspace: "/repos/app",
		Model:     "sonnet",
		Effort:    "high",
		Priority:  7,
		Steps:     []string{"extract the interface", "swap the implementation", "update the tests"},
	}
	created, err := q.Enqueue(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}

	children, err := q.PromoteSteps(ctx, created.ID)
	if err != nil {
		t.Fatalf("PromoteSteps() error: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("PromoteSteps() returned %d children, want one per declared step (3)", len(children))
	}

	for i, child := range children {
		// Re-read rather than trusting the returned value: ParentJobID is a
		// newly added column, so this also proves it actually persisted.
		got, err := q.Get(ctx, child.ID)
		if err != nil {
			t.Fatalf("Get(child %d) error: %v", i, err)
		}
		if got.Prompt != parent.Steps[i] {
			t.Errorf("child %d Prompt = %q, want the declared step %q", i, got.Prompt, parent.Steps[i])
		}
		if got.ParentJobID != created.ID {
			t.Errorf("child %d ParentJobID = %q, want the promoted parent %q", i, got.ParentJobID, created.ID)
		}
		if got.Resumable != created.Resumable {
			t.Errorf("child %d Resumable = %v, want the parent's %v — promotion must not change the interruptibility guarantee the user set", i, got.Resumable, created.Resumable)
		}
		if got.Kind != parent.Kind || got.Model != parent.Model || got.Effort != parent.Effort ||
			got.Workspace != parent.Workspace || got.Priority != parent.Priority {
			t.Errorf("child %d did not inherit the parent's execution settings: got kind=%q model=%q effort=%q workspace=%q priority=%d, want kind=%q model=%q effort=%q workspace=%q priority=%d",
				i, got.Kind, got.Model, got.Effort, got.Workspace, got.Priority,
				parent.Kind, parent.Model, parent.Effort, parent.Workspace, parent.Priority)
		}

		if i == 0 {
			if len(got.DependsOn) != 0 {
				t.Errorf("child 0 DependsOn = %v, want none (the head of the chain runs immediately)", got.DependsOn)
			}
			if got.Status != store.StatusQueued {
				t.Errorf("child 0 Status = %q, want %q", got.Status, store.StatusQueued)
			}
			continue
		}
		if len(got.DependsOn) != 1 || got.DependsOn[0] != children[i-1].ID {
			t.Errorf("child %d DependsOn = %v, want [%s] (the previous step)", i, got.DependsOn, children[i-1].ID)
		}
		if got.Status != store.StatusBlocked {
			t.Errorf("child %d Status = %q, want %q until the previous step succeeds", i, got.Status, store.StatusBlocked)
		}
	}

	promoted, err := q.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != store.StatusPromoted {
		t.Errorf("parent Status = %q, want %q (its work now lives in the children)", promoted.Status, store.StatusPromoted)
	}
	if promoted.FailureReason == "" {
		t.Fatal("parent FailureReason is empty, want it naming the children it became")
	}
	for _, child := range children {
		if !strings.Contains(promoted.FailureReason, child.ID) {
			t.Errorf("parent FailureReason = %q, want it to name child %s so the promotion stays traceable", promoted.FailureReason, child.ID)
		}
	}

	// A promoted parent is terminal, so it must never come back as a
	// dispatch candidate alongside its own children.
	candidates, err := q.Candidates(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != children[0].ID {
		t.Errorf("Candidates() = %+v, want only the first step child %s", candidates, children[0].ID)
	}
}

// TestPromoteSteps_ChildrenInheritSafetyFields is the FR-SAFE-3 guard on
// promotion: a child that loses the parent's AllowedTools or PermissionMode
// doesn't just lose a preference, it gets internal/runner/safety.go's
// kind-default instead (Bash/Write for code, auto-approve for freeform), so
// promotion would silently widen what an unattended step may do. Losing
// MaxBudgetUSD would likewise defeat FR-SAFE-2's per-job spend cap.
func TestPromoteSteps_ChildrenInheritSafetyFields(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	budgetCap := 4.25
	deadline := time.Now().UTC().Add(6 * time.Hour).Truncate(time.Second)
	parent := store.Job{
		Kind:             store.JobKindFreeform,
		Prompt:           "tidy the repo",
		Workspace:        "/repos/app",
		Steps:            []string{"first", "second"},
		AllowedTools:     []string{"Read"},
		PermissionMode:   "ask",
		AddDirs:          []string{"/repos/shared"},
		FreeformWorktree: true,
		MaxBudgetUSD:     &budgetCap,
		Attachments:      []store.Attachment{{Path: "/tmp/spec.md", Name: "spec.md"}},
		JSONSchema:       `{"type":"object"}`,
		DeadlineAt:       &deadline,
	}
	created, err := q.Enqueue(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}

	children, err := q.PromoteSteps(ctx, created.ID)
	if err != nil {
		t.Fatalf("PromoteSteps() error: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("PromoteSteps() returned %d children, want 2", len(children))
	}

	for i, child := range children {
		got, err := q.Get(ctx, child.ID)
		if err != nil {
			t.Fatalf("Get(child %d) error: %v", i, err)
		}
		if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "Read" {
			t.Errorf("child %d AllowedTools = %v, want the parent's [Read] — an empty allowlist means the runner applies the kind default instead", i, got.AllowedTools)
		}
		if got.PermissionMode != "ask" {
			t.Errorf("child %d PermissionMode = %q, want the parent's %q — empty means the runner auto-approves a freeform job", i, got.PermissionMode, "ask")
		}
		if len(got.AddDirs) != 1 || got.AddDirs[0] != "/repos/shared" {
			t.Errorf("child %d AddDirs = %v, want the parent's [/repos/shared]", i, got.AddDirs)
		}
		if !got.FreeformWorktree {
			t.Errorf("child %d FreeformWorktree = false, want the parent's true", i)
		}
		if got.MaxBudgetUSD == nil || *got.MaxBudgetUSD != budgetCap {
			t.Errorf("child %d MaxBudgetUSD = %v, want the parent's %v (FR-SAFE-2)", i, got.MaxBudgetUSD, budgetCap)
		}
		if len(got.Attachments) != 1 || got.Attachments[0].Path != "/tmp/spec.md" {
			t.Errorf("child %d Attachments = %v, want the parent's single /tmp/spec.md", i, got.Attachments)
		}
		if got.JSONSchema != parent.JSONSchema {
			t.Errorf("child %d JSONSchema = %q, want the parent's %q", i, got.JSONSchema, parent.JSONSchema)
		}
		if got.DeadlineAt == nil || !got.DeadlineAt.Equal(deadline) {
			t.Errorf("child %d DeadlineAt = %v, want the parent's %v", i, got.DeadlineAt, deadline)
		}
		if got.EarliestAt != nil {
			t.Errorf("child %d EarliestAt = %v, want nil — a child's turn comes from the dependency chain, not a copied timestamp", i, got.EarliestAt)
		}
	}
}

// TestPromoteSteps_FirstChildInheritsParentSession covers §6.4 strategy 2's
// "children share the parent's session" for the head of the chain: a parent
// that already has a SessionID (it ran and paused at a budget cap before
// being found oversized) has paid for that context, so the first step
// continues it via --resume rather than starting cold.
func TestPromoteSteps_FirstChildInheritsParentSession(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	parent := minimalJob()
	parent.Steps = []string{"step one", "step two"}
	created, err := q.Enqueue(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSessionID(ctx, created.ID, "sess-parent-001"); err != nil {
		t.Fatal(err)
	}

	children, err := q.PromoteSteps(ctx, created.ID)
	if err != nil {
		t.Fatalf("PromoteSteps() error: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("PromoteSteps() returned %d children, want 2", len(children))
	}

	first, err := q.Get(ctx, children[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID != "sess-parent-001" {
		t.Errorf("first child SessionID = %q, want the parent's %q so the chain resumes the session the parent already paid for", first.SessionID, "sess-parent-001")
	}

	// Only the head inherits directly; later children get their session from
	// the step that actually ran before them (PromoteReady's inheritance).
	second, err := q.Get(ctx, children[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != "" {
		t.Errorf("second child SessionID = %q, want empty until the step before it succeeds and hands its session over", second.SessionID)
	}
}

// TestPromoteSteps_IsIdempotent covers the duplicate-spend failure mode: a
// promotion that created children but failed before recording the parent as
// Promoted must not create a second set of children when the scheduler
// retries it 30 seconds later — it must adopt the ones already there and
// finish marking the parent.
func TestPromoteSteps_IsIdempotent(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	parent := minimalJob()
	parent.Steps = []string{"step one", "step two", "step three"}
	created, err := q.Enqueue(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}

	first, err := q.PromoteSteps(ctx, created.ID)
	if err != nil {
		t.Fatalf("PromoteSteps() error: %v", err)
	}

	second, err := q.PromoteSteps(ctx, created.ID)
	if err != nil {
		t.Fatalf("second PromoteSteps() error: %v", err)
	}
	assertSameChain(t, "second call", first, second)

	// Simulate the real failure mode: children landed, the final status
	// write didn't, so the parent is still runnable and the scheduler tries
	// again.
	if err := s.UpdateStatus(ctx, created.ID, store.StatusQueued, ""); err != nil {
		t.Fatal(err)
	}
	third, err := q.PromoteSteps(ctx, created.ID)
	if err != nil {
		t.Fatalf("third PromoteSteps() error: %v", err)
	}
	assertSameChain(t, "retry after a failed status write", first, third)

	all, err := q.List(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("store holds %d jobs after three PromoteSteps calls, want 4 (the parent plus one chain of 3) — repeated promotion must not duplicate children", len(all))
	}

	got, err := q.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusPromoted {
		t.Errorf("parent Status = %q, want %q — the idempotent path must self-heal the status write that previously failed", got.Status, store.StatusPromoted)
	}
	for _, child := range first {
		if !strings.Contains(got.FailureReason, child.ID) {
			t.Errorf("parent FailureReason = %q, want it to name the existing child %s", got.FailureReason, child.ID)
		}
	}
}

func assertSameChain(t *testing.T, label string, want, got []store.Job) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s returned %d children, want the original %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Errorf("%s child %d ID = %s, want the original %s (in chain order)", label, i, got[i].ID, want[i].ID)
		}
	}
}

// TestPromoteSteps_RewiresBlockedDependentsOntoLastChild covers the other
// half of Promoted being terminal: an unrelated job waiting on the promoted
// parent must not be cancelled as if the parent had failed. It waits for
// the chain's last step instead, which is what "after that job's work"
// actually means now.
func TestPromoteSteps_RewiresBlockedDependentsOntoLastChild(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	a := minimalJob()
	a.Steps = []string{"step one", "step two"}
	parent, err := q.Enqueue(ctx, a)
	if err != nil {
		t.Fatal(err)
	}

	b := minimalJob()
	b.Prompt = "runs after A"
	b.DependsOn = []string{parent.ID}
	dependent, err := q.Enqueue(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if dependent.Status != store.StatusBlocked {
		t.Fatalf("dependent Status = %q, want %q", dependent.Status, store.StatusBlocked)
	}

	children, err := q.PromoteSteps(ctx, parent.ID)
	if err != nil {
		t.Fatalf("PromoteSteps() error: %v", err)
	}
	lastChild := children[len(children)-1]

	got, err := q.Get(ctx, dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DependsOn) != 1 || got.DependsOn[0] != lastChild.ID {
		t.Errorf("dependent DependsOn = %v, want [%s] (the chain's last step, not the promoted parent)", got.DependsOn, lastChild.ID)
	}
	if got.Status != store.StatusBlocked {
		t.Fatalf("dependent Status = %q, want it still %q — a promotion is not a failure", got.Status, store.StatusBlocked)
	}

	// The sweep must leave it alone while the chain is still running...
	if _, err := q.PromoteReady(ctx); err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	got, err = q.Get(ctx, dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusBlocked {
		t.Fatalf("dependent Status = %q after a sweep, want still %q (reason: %q)", got.Status, store.StatusBlocked, got.FailureReason)
	}

	// ...and release it once the chain's last step actually succeeds.
	for i, child := range children {
		if i > 0 {
			// Only the head starts Queued; a sweep releases each next step.
			if _, err := q.PromoteReady(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := q.MarkRunning(ctx, child.ID); err != nil {
			t.Fatalf("MarkRunning(child %d): %v", i, err)
		}
		if err := q.Finish(ctx, child.ID, FinishOutcome{Status: store.StatusSucceeded}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.PromoteReady(ctx); err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}

	got, err = q.Get(ctx, dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("dependent Status = %q after the chain's last step succeeded, want %q (reason: %q)", got.Status, store.StatusQueued, got.FailureReason)
	}
}

func TestPromoteSteps_NoSteps_Errors(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob()) // no Steps declared
	if err != nil {
		t.Fatal(err)
	}

	if _, err := q.PromoteSteps(ctx, j.ID); !errors.Is(err, ErrNoSteps) {
		t.Errorf("PromoteSteps() error = %v, want ErrNoSteps", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status = %q after a rejected PromoteSteps, want unchanged %q", got.Status, store.StatusQueued)
	}
}

func TestPromoteSteps_AlreadyTerminal_Errors(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j := minimalJob()
	j.Steps = []string{"step one", "step two"}
	created, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, created.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := q.PromoteSteps(ctx, created.ID); !errors.Is(err, ErrAlreadyTerminal) {
		t.Errorf("PromoteSteps() error = %v, want ErrAlreadyTerminal", err)
	}
	all, err := q.List(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("store holds %d jobs after a rejected PromoteSteps, want only the original 1 (no orphan children)", len(all))
	}
}

func TestNextRunnable_OrdersByPriority(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	low := minimalJob()
	low.Priority = 1
	if _, err := q.Enqueue(ctx, low); err != nil {
		t.Fatal(err)
	}

	high := minimalJob()
	high.Priority = 10
	highJob, err := q.Enqueue(ctx, high)
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := q.NextRunnable(ctx, time.Now())
	if err != nil {
		t.Fatalf("NextRunnable() error: %v", err)
	}
	if !ok {
		t.Fatal("NextRunnable() ok = false, want true")
	}
	if got.ID != highJob.ID {
		t.Errorf("NextRunnable() = %s, want highest-priority job %s", got.ID, highJob.ID)
	}
}

func TestNextRunnable_RespectsEarliestAt(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()

	future := now.Add(time.Hour)
	notYet := minimalJob()
	notYet.Priority = 100 // highest priority, but not eligible yet
	notYet.EarliestAt = &future
	if _, err := q.Enqueue(ctx, notYet); err != nil {
		t.Fatal(err)
	}

	ready := minimalJob()
	ready.Priority = 1
	readyJob, err := q.Enqueue(ctx, ready)
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := q.NextRunnable(ctx, now)
	if err != nil {
		t.Fatalf("NextRunnable() error: %v", err)
	}
	if !ok {
		t.Fatal("NextRunnable() ok = false, want true (the eligible lower-priority job)")
	}
	if got.ID != readyJob.ID {
		t.Errorf("NextRunnable() = %s, want %s (EarliestAt job should be skipped)", got.ID, readyJob.ID)
	}
}

func TestNextRunnable_NoneEligible(t *testing.T) {
	q, _ := newTestQueue(t)
	got, ok, err := q.NextRunnable(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("NextRunnable() error: %v", err)
	}
	if ok {
		t.Errorf("NextRunnable() on empty queue: ok = true, got %+v", got)
	}
}

func TestCandidates_IncludesPausedBudget(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	paused := minimalJob()
	paused.Priority = 5
	pausedJob, err := q.Enqueue(ctx, paused)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, pausedJob.ID, store.StatusPausedBudget, ""); err != nil {
		t.Fatal(err)
	}

	fresh := minimalJob()
	fresh.Priority = 1
	freshJob, err := q.Enqueue(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}

	got, err := q.Candidates(ctx, time.Now())
	if err != nil {
		t.Fatalf("Candidates() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Candidates() returned %d jobs, want 2 (got %+v)", len(got), got)
	}
	// Higher priority (the paused_budget job) sorts first, same as any other
	// runnable job — a resumable job's cap-and-continue is just as eligible
	// as a fresh dispatch (§6.4 strategy 1).
	if got[0].ID != pausedJob.ID {
		t.Errorf("Candidates()[0] = %s, want the higher-priority paused_budget job %s", got[0].ID, pausedJob.ID)
	}
	if got[1].ID != freshJob.ID {
		t.Errorf("Candidates()[1] = %s, want %s", got[1].ID, freshJob.ID)
	}
}

func TestCapBudget_SetsMaxBudget(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CapBudget(ctx, j.ID, 1.5); err != nil {
		t.Fatalf("CapBudget() error: %v", err)
	}

	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxBudgetUSD == nil || *got.MaxBudgetUSD != 1.5 {
		t.Errorf("MaxBudgetUSD = %v, want 1.5", got.MaxBudgetUSD)
	}
}

func TestCapBudget_AlreadyTerminal_Errors(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	if err := q.CapBudget(ctx, j.ID, 1.5); !errors.Is(err, ErrAlreadyTerminal) {
		t.Errorf("CapBudget() error = %v, want ErrAlreadyTerminal", err)
	}
}

func TestCancel_FromQueued(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusCancelled {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusCancelled)
	}
}

func TestCancel_AlreadyTerminal_Errors(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	if err := q.Cancel(ctx, j.ID); !errors.Is(err, ErrAlreadyTerminal) {
		t.Errorf("Cancel() error = %v, want ErrAlreadyTerminal", err)
	}
}

func TestDeferOversized_FromQueued(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.DeferOversized(ctx, j.ID, "exceeds remaining 5h headroom even empty"); err != nil {
		t.Fatalf("DeferOversized() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusDeferredOversized {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusDeferredOversized)
	}
	if got.FailureReason == "" {
		t.Error("FailureReason is empty, want the deferral reason recorded")
	}
}

func TestDeferOversized_AlreadyTerminal_Errors(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	if err := q.DeferOversized(ctx, j.ID, "too big"); !errors.Is(err, ErrAlreadyTerminal) {
		t.Errorf("DeferOversized() error = %v, want ErrAlreadyTerminal", err)
	}
}

func TestMarkRunning_FromQueued(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	before, err := q.MarkRunning(ctx, j.ID)
	if err != nil {
		t.Fatalf("MarkRunning() error: %v", err)
	}
	if before.Status != store.StatusQueued {
		t.Errorf("MarkRunning() returned job with Status = %q, want the pre-transition %q", before.Status, store.StatusQueued)
	}

	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusRunning {
		t.Errorf("Status after MarkRunning = %q, want %q", got.Status, store.StatusRunning)
	}
}

func TestMarkRunning_FromPausedBudget(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusPausedBudget, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := q.MarkRunning(ctx, j.ID); err != nil {
		t.Fatalf("MarkRunning() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusRunning)
	}
}

func TestMarkRunning_NotRunnable(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := q.MarkRunning(ctx, j.ID); !errors.Is(err, ErrNotRunnable) {
		t.Errorf("MarkRunning() error = %v, want ErrNotRunnable", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status changed to %q after a rejected MarkRunning, want unchanged %q", got.Status, store.StatusSucceeded)
	}
}

func TestFinish_RecordsStatusAndSessionID(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkRunning(ctx, j.ID); err != nil {
		t.Fatal(err)
	}

	if err := q.Finish(ctx, j.ID, FinishOutcome{Status: store.StatusSucceeded, SessionID: "sess-123"}); err != nil {
		t.Fatalf("Finish() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusSucceeded)
	}
	if got.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-123")
	}
}

func TestFinish_FailureReasonWithoutSessionID(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkRunning(ctx, j.ID); err != nil {
		t.Fatal(err)
	}

	if err := q.Finish(ctx, j.ID, FinishOutcome{Status: store.StatusFailed, FailureReason: "claude exited without a result"}); err != nil {
		t.Fatalf("Finish() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusFailed)
	}
	if got.FailureReason != "claude exited without a result" {
		t.Errorf("FailureReason = %q, want the recorded reason", got.FailureReason)
	}
	if got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty when Finish was called with no sessionID", got.SessionID)
	}
}

func TestFinish_RecordsResult(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkRunning(ctx, j.ID); err != nil {
		t.Fatal(err)
	}

	if err := q.Finish(ctx, j.ID, FinishOutcome{Status: store.StatusSucceeded, Result: "the answer is 42"}); err != nil {
		t.Fatalf("Finish() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "the answer is 42" {
		t.Errorf("Result = %q, want %q", got.Result, "the answer is 42")
	}
}
