package runner

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"tokenwarden/internal/store"
)

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// flagValue returns the value immediately following flag in args, if
// present.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestBuildArgs_KindProfiles(t *testing.T) {
	tests := []struct {
		name         string
		job          store.Job
		wantMode     string
		wantWorktree bool
		wantErr      bool
	}{
		{
			name:     "research",
			job:      store.Job{Kind: store.JobKindResearch, Prompt: "summarize the changelog"},
			wantMode: "dontAsk",
		},
		{
			name:     "plan",
			job:      store.Job{Kind: store.JobKindPlan, Prompt: "propose a migration plan"},
			wantMode: "plan",
		},
		{
			name:         "code",
			job:          store.Job{Kind: store.JobKindCode, Prompt: "fix the bug", Workspace: "/repos/app"},
			wantMode:     "acceptEdits",
			wantWorktree: true,
		},
		{
			name:    "code without workspace errors",
			job:     store.Job{Kind: store.JobKindCode, Prompt: "fix the bug"},
			wantErr: true,
		},
		{
			name:     "review",
			job:      store.Job{Kind: store.JobKindReview, Prompt: "review this PR"},
			wantMode: "dontAsk",
		},
		{
			name:     "freeform defaults to dontAsk",
			job:      store.Job{Kind: store.JobKindFreeform, Prompt: "do something"},
			wantMode: "dontAsk",
		},
		{
			name:     "freeform explicit valid mode",
			job:      store.Job{Kind: store.JobKindFreeform, Prompt: "do something", PermissionMode: "acceptEdits", FreeformWorktree: true},
			wantMode: "acceptEdits", wantWorktree: true,
		},
		{
			name:    "freeform rejects bypassPermissions",
			job:     store.Job{Kind: store.JobKindFreeform, Prompt: "do something", PermissionMode: "bypassPermissions"},
			wantErr: true,
		},
		{
			name:    "freeform rejects dangerously-skip-permissions string",
			job:     store.Job{Kind: store.JobKindFreeform, Prompt: "do something", PermissionMode: "--dangerously-skip-permissions"},
			wantErr: true,
		},
		{
			name:    "unknown kind errors",
			job:     store.Job{Kind: "not-a-real-kind", Prompt: "x"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := BuildArgs(tt.job)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("BuildArgs() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildArgs() error = %v", err)
			}

			mode, ok := flagValue(args, "--permission-mode")
			if !ok || mode != tt.wantMode {
				t.Errorf("--permission-mode = %q (present=%v), want %q", mode, ok, tt.wantMode)
			}
			if hasArg(args, "--worktree") != tt.wantWorktree {
				t.Errorf("--worktree present = %v, want %v (args: %v)", hasArg(args, "--worktree"), tt.wantWorktree, args)
			}
		})
	}
}

// TestBuildArgs_NeverSkipsPermissions is the durable structural guarantee
// check (FR-SAFE-1): across every case above plus a few more adversarial
// freeform inputs, no generated argv may ever contain the string that maps
// to --dangerously-skip-permissions.
func TestBuildArgs_NeverSkipsPermissions(t *testing.T) {
	jobs := []store.Job{
		{Kind: store.JobKindResearch, Prompt: "x"},
		{Kind: store.JobKindPlan, Prompt: "x"},
		{Kind: store.JobKindCode, Prompt: "x", Workspace: "/repos/app"},
		{Kind: store.JobKindReview, Prompt: "x"},
		{Kind: store.JobKindFreeform, Prompt: "x"},
		{Kind: store.JobKindFreeform, Prompt: "x", PermissionMode: "acceptEdits"},
		{Kind: store.JobKindFreeform, Prompt: "x", PermissionMode: "bypassPermissions"},
		{Kind: store.JobKindFreeform, Prompt: "x", PermissionMode: "--dangerously-skip-permissions"},
		{Kind: store.JobKindFreeform, Prompt: "x", PermissionMode: "plan; --dangerously-skip-permissions"},
	}
	for _, j := range jobs {
		args, err := BuildArgs(j)
		if err != nil {
			continue // rejected outright, which is fine — nothing to check
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "dangerously-skip-permissions") || strings.Contains(joined, "bypassPermissions") {
			t.Errorf("BuildArgs(%+v) produced args containing a skip-permissions flag: %v", j, args)
		}
	}
}

func TestBuildArgs_FreeformUnsupportedModeIsSentinel(t *testing.T) {
	job := store.Job{Kind: store.JobKindFreeform, Prompt: "x", PermissionMode: "bypassPermissions"}
	_, err := BuildArgs(job)
	if !errors.Is(err, ErrUnsupportedPermissionMode) {
		t.Errorf("BuildArgs() error = %v, want ErrUnsupportedPermissionMode", err)
	}
}

func TestBuildArgs_ResumableForcesWorktreeAndProgressInstruction(t *testing.T) {
	job := store.Job{Kind: store.JobKindResearch, Prompt: "long research task", Resumable: true}
	args, err := BuildArgs(job)
	if err != nil {
		t.Fatalf("BuildArgs() error: %v", err)
	}
	if !hasArg(args, "--worktree") {
		t.Errorf("resumable research job: --worktree missing, args: %v", args)
	}
	prompt, ok := flagValue(args, "-p")
	if !ok || !strings.Contains(prompt, "PROGRESS.md") {
		t.Errorf("resumable job prompt missing PROGRESS.md instruction: %q", prompt)
	}
}

func TestBuildArgs_SessionIDEmptyGeneratesFreshUUID(t *testing.T) {
	job := store.Job{Kind: store.JobKindResearch, Prompt: "x"}
	args, err := BuildArgs(job)
	if err != nil {
		t.Fatalf("BuildArgs() error: %v", err)
	}
	if hasArg(args, "--resume") {
		t.Errorf("empty SessionID: --resume should not be present, args: %v", args)
	}
	id, ok := flagValue(args, "--session-id")
	if !ok {
		t.Fatalf("--session-id missing, args: %v", args)
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("--session-id value %q is not a valid UUID: %v", id, err)
	}
}

func TestBuildArgs_SessionIDSetUsesResume(t *testing.T) {
	job := store.Job{Kind: store.JobKindResearch, Prompt: "x", SessionID: "existing-session-id"}
	args, err := BuildArgs(job)
	if err != nil {
		t.Fatalf("BuildArgs() error: %v", err)
	}
	if hasArg(args, "--session-id") {
		t.Errorf("SessionID set: --session-id should not be present, args: %v", args)
	}
	id, ok := flagValue(args, "--resume")
	if !ok || id != "existing-session-id" {
		t.Errorf("--resume = %q (present=%v), want %q", id, ok, "existing-session-id")
	}
}

func TestBuildArgs_MaxBudgetAndJSONSchema(t *testing.T) {
	budget := 2.5
	job := store.Job{
		Kind:         store.JobKindResearch,
		Prompt:       "x",
		MaxBudgetUSD: &budget,
		JSONSchema:   `{"type":"object"}`,
	}
	args, err := BuildArgs(job)
	if err != nil {
		t.Fatalf("BuildArgs() error: %v", err)
	}
	v, ok := flagValue(args, "--max-budget-usd")
	if !ok || v != "2.5" {
		t.Errorf("--max-budget-usd = %q (present=%v), want %q", v, ok, "2.5")
	}
	schema, ok := flagValue(args, "--json-schema")
	if !ok || schema != `{"type":"object"}` {
		t.Errorf("--json-schema = %q (present=%v), want %q", schema, ok, `{"type":"object"}`)
	}
}

func TestBuildArgs_CodeUsesJobAllowedToolsOverride(t *testing.T) {
	job := store.Job{
		Kind:         store.JobKindCode,
		Prompt:       "x",
		Workspace:    "/repos/app",
		AllowedTools: []string{"Read", "Edit"},
	}
	args, err := BuildArgs(job)
	if err != nil {
		t.Fatalf("BuildArgs() error: %v", err)
	}
	tools, ok := flagValue(args, "--allowedTools")
	if !ok || tools != "Read,Edit" {
		t.Errorf("--allowedTools = %q (present=%v), want %q", tools, ok, "Read,Edit")
	}
}
