package runner

import (
	"fmt"

	"tokenwarden/internal/store"
)

// PermissionProfile is the resolved autonomy posture for one dispatch:
// which --permission-mode, --allowedTools, --worktree, and --add-dir flags
// the job runs under. It is produced by profileFor and consumed only by
// BuildArgs — nothing else in this package or its callers constructs one
// directly, which keeps FR-SAFE-3 enforcement in exactly one place.
type PermissionProfile struct {
	Mode         string
	AllowedTools []string
	Worktree     bool
	AddDirs      []string
}

// readOnlyTools backs the research/plan/review kinds: they observe and
// propose, never edit (FR-SAFE-3).
var readOnlyTools = []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch", "NotebookRead"}

// defaultCodeTools is JobKindCode's tool set when the job doesn't name its
// own AllowedTools.
var defaultCodeTools = []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob"}

// permissionModeAllowlist is the only place a --permission-mode string is
// validated against, including for freeform jobs. "bypassPermissions" (the
// mode --dangerously-skip-permissions maps to) is deliberately absent, and
// there is no config key, job field, or code path anywhere in this package
// that can add to this set at runtime (FR-SAFE-1).
var permissionModeAllowlist = map[string]bool{
	"plan":        true,
	"dontAsk":     true,
	"acceptEdits": true,
	"default":     true,
}

// profileFor maps a job's kind (and, for freeform, its own declared
// fields) to a PermissionProfile per REQUIREMENTS.md §8 / FR-SAFE-3:
//
//	research  dontAsk,      read-only tools    no worktree
//	plan      plan,         read-only tools    no worktree
//	code      acceptEdits,  code tools         worktree (always)
//	review    dontAsk,      read-only tools    no worktree
//	freeform  user-chosen,  user-chosen        user-chosen
//
// Every path but freeform ignores the job's own PermissionMode/AllowedTools
// fields entirely — a fixed-kind job's autonomy posture is not
// user-overridable, by design.
func profileFor(job store.Job) (PermissionProfile, error) {
	switch job.Kind {
	case store.JobKindResearch, store.JobKindReview:
		return PermissionProfile{Mode: "dontAsk", AllowedTools: readOnlyTools}, nil

	case store.JobKindPlan:
		return PermissionProfile{Mode: "plan", AllowedTools: readOnlyTools}, nil

	case store.JobKindCode:
		if job.Workspace == "" {
			return PermissionProfile{}, fmt.Errorf("code job requires a workspace to create a --worktree from")
		}
		tools := job.AllowedTools
		if len(tools) == 0 {
			tools = defaultCodeTools
		}
		return PermissionProfile{Mode: "acceptEdits", AllowedTools: tools, Worktree: true}, nil

	case store.JobKindFreeform:
		mode := job.PermissionMode
		if mode == "" {
			mode = "dontAsk" // conservative default when unspecified
		}
		if !permissionModeAllowlist[mode] {
			return PermissionProfile{}, fmt.Errorf("%w: %q", ErrUnsupportedPermissionMode, mode)
		}
		return PermissionProfile{
			Mode:         mode,
			AllowedTools: job.AllowedTools,
			Worktree:     job.FreeformWorktree,
			AddDirs:      job.AddDirs,
		}, nil

	default:
		return PermissionProfile{}, fmt.Errorf("unknown job kind %q", job.Kind)
	}
}
