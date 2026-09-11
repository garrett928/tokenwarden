package store

import "time"

// JobKind determines the CLI flag profile a job runs under — this is where
// autonomy posture lives (REQUIREMENTS.md §8.2 / FR-SAFE-3). The runner
// (Phase 3) maps each kind to a --permission-mode and tool allowlist; the
// store just persists the choice.
type JobKind string

const (
	JobKindResearch JobKind = "research"
	JobKindPlan     JobKind = "plan"
	JobKindCode     JobKind = "code"
	JobKindReview   JobKind = "review"
	JobKindFreeform JobKind = "freeform"
)

// ValidJobKinds lists every kind the store will accept on create.
var ValidJobKinds = []JobKind{JobKindResearch, JobKindPlan, JobKindCode, JobKindReview, JobKindFreeform}

func (k JobKind) valid() bool {
	for _, v := range ValidJobKinds {
		if k == v {
			return true
		}
	}
	return false
}

// Status is a job's lifecycle state (REQUIREMENTS.md FR-JOB-8).
type Status string

const (
	StatusQueued            Status = "queued"
	StatusBlocked           Status = "blocked" // waiting on DependsOn
	StatusRunning           Status = "running"
	StatusPausedBudget      Status = "paused_budget"      // hit --max-budget-usd, awaiting --resume
	StatusDeferredOversized Status = "deferred_oversized" // couldn't be fit; see REQUIREMENTS.md §6.4
	// StatusPromoted means Steps were promoted into child jobs (§6.4
	// strategy 2); this job itself never dispatches.
	StatusPromoted  Status = "promoted"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Terminal reports whether a job in this status will never transition again
// on its own. Used by dependency gating: only a Succeeded dependency
// unblocks a downstream job, but Failed/Cancelled/Promoted are also terminal
// for purposes of deciding a blocked job will never become runnable.
//
// Promoted is the one terminal status that does not mean the work died: a
// promoted parent's work continues in its children. queue.computeStatus
// would therefore cancel its dependents as if it had failed, so
// queue.PromoteSteps rewires those dependents onto the chain's last child
// before the next PromoteReady sweep ever sees the promoted parent — the
// special case lives there, not in computeStatus.
func (s Status) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusPromoted:
		return true
	default:
		return false
	}
}

// Attachment is a file or screenshot copied into a job's workspace and
// referenced by absolute path in the prompt (FR-JOB-5). tokenwarden does
// not encode image bytes itself — Claude Code's Read tool handles images
// and PDFs natively from a path.
type Attachment struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// Job is a unit of queued work. See REQUIREMENTS.md §5.1 (FR-JOB-1..8) and
// §6.4 for how Steps and Resumable are used when a job doesn't fit the
// remaining 5-hour window.
type Job struct {
	ID        string
	Kind      JobKind
	Prompt    string
	Workspace string
	Model     string
	Effort    string

	Attachments []Attachment
	Steps       []string // optional user-declared split points, §6.4 strategy 2
	Resumable   bool     // may be budget-capped and --resume'd later, §6.4 strategy 1

	// PermissionMode, AllowedTools, AddDirs, and FreeformWorktree only apply
	// to JobKindFreeform, whose autonomy posture is "user-specified" rather
	// than fixed by kind (FR-SAFE-3). The store does no semantic validation
	// of them — same as everywhere else in this file, the runner (Phase 3)
	// is what enforces the safety table these feed into.
	PermissionMode   string
	AllowedTools     []string
	AddDirs          []string
	FreeformWorktree bool

	// JSONSchema is passed as --json-schema for structured results,
	// primarily useful on JobKindResearch (FR-JOB-2, §4.2).
	JSONSchema string

	Priority     int
	EarliestAt   *time.Time
	DeadlineAt   *time.Time
	MaxBudgetUSD *float64
	DependsOn    []string

	SessionID string // set after first dispatch; enables --resume
	// ParentJobID is set when this job was created by promoting another
	// job's Steps (§6.4 strategy 2); empty for an ordinary job.
	ParentJobID string

	Status        Status
	FailureReason string
	// Result is the runner's final text output (runner.Result.Result) —
	// set on both success and failure, since even a failed run's summary
	// text is useful diagnostic information. Empty until the job completes.
	Result string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListFilter narrows ListJobs. A zero-value ListFilter returns every job.
type ListFilter struct {
	// Statuses restricts results to these statuses. Empty means no filter.
	Statuses []Status
	// ParentJobID restricts results to the children of one promoted parent
	// (§6.4 strategy 2). Empty means no filter, exactly like an empty
	// Statuses; both may be set at once and are ANDed together.
	ParentJobID string
}
