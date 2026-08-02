package api

import (
	"time"

	"tokenwarden/internal/store"
)

// The wire format is deliberately kept separate from store.Job — a JSON
// contract that happens to match today's internal struct is a coincidence
// that breaks the moment either side changes for its own reasons. snake_case
// tags here, Go-idiomatic names in store.Job.

type Attachment struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// CreateJobRequest is the accepted body for POST /api/jobs. It intentionally
// excludes server-assigned fields (id, status, session_id, timestamps) —
// a client can't set a job's status by hand.
type CreateJobRequest struct {
	Kind         string       `json:"kind"`
	Prompt       string       `json:"prompt"`
	Workspace    string       `json:"workspace,omitempty"`
	Model        string       `json:"model,omitempty"`
	Effort       string       `json:"effort,omitempty"`
	Attachments  []Attachment `json:"attachments,omitempty"`
	Steps        []string     `json:"steps,omitempty"`
	Resumable    bool         `json:"resumable,omitempty"`
	Priority     int          `json:"priority,omitempty"`
	EarliestAt   *time.Time   `json:"earliest_at,omitempty"`
	DeadlineAt   *time.Time   `json:"deadline_at,omitempty"`
	MaxBudgetUSD *float64     `json:"max_budget_usd,omitempty"`
	DependsOn    []string     `json:"depends_on,omitempty"`
}

func (r CreateJobRequest) toJob() store.Job {
	return store.Job{
		Kind:         store.JobKind(r.Kind),
		Prompt:       r.Prompt,
		Workspace:    r.Workspace,
		Model:        r.Model,
		Effort:       r.Effort,
		Attachments:  toStoreAttachments(r.Attachments),
		Steps:        r.Steps,
		Resumable:    r.Resumable,
		Priority:     r.Priority,
		EarliestAt:   r.EarliestAt,
		DeadlineAt:   r.DeadlineAt,
		MaxBudgetUSD: r.MaxBudgetUSD,
		DependsOn:    r.DependsOn,
	}
}

// JobResponse is the full representation returned by GET/POST job endpoints.
type JobResponse struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Prompt    string `json:"prompt"`
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`

	Attachments []Attachment `json:"attachments"`
	Steps       []string     `json:"steps"`
	Resumable   bool         `json:"resumable"`

	Priority     int        `json:"priority"`
	EarliestAt   *time.Time `json:"earliest_at,omitempty"`
	DeadlineAt   *time.Time `json:"deadline_at,omitempty"`
	MaxBudgetUSD *float64   `json:"max_budget_usd,omitempty"`
	DependsOn    []string   `json:"depends_on"`

	SessionID string `json:"session_id,omitempty"`

	Status        string `json:"status"`
	FailureReason string `json:"failure_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newJobResponse(j store.Job) JobResponse {
	return JobResponse{
		ID:            j.ID,
		Kind:          string(j.Kind),
		Prompt:        j.Prompt,
		Workspace:     j.Workspace,
		Model:         j.Model,
		Effort:        j.Effort,
		Attachments:   toDTOAttachments(j.Attachments),
		Steps:         j.Steps,
		Resumable:     j.Resumable,
		Priority:      j.Priority,
		EarliestAt:    j.EarliestAt,
		DeadlineAt:    j.DeadlineAt,
		MaxBudgetUSD:  j.MaxBudgetUSD,
		DependsOn:     j.DependsOn,
		SessionID:     j.SessionID,
		Status:        string(j.Status),
		FailureReason: j.FailureReason,
		CreatedAt:     j.CreatedAt,
		UpdatedAt:     j.UpdatedAt,
	}
}

func toStoreAttachments(in []Attachment) []store.Attachment {
	if in == nil {
		return nil
	}
	out := make([]store.Attachment, len(in))
	for i, a := range in {
		out[i] = store.Attachment{Path: a.Path, Name: a.Name}
	}
	return out
}

func toDTOAttachments(in []store.Attachment) []Attachment {
	if in == nil {
		return []Attachment{}
	}
	out := make([]Attachment, len(in))
	for i, a := range in {
		out[i] = Attachment{Path: a.Path, Name: a.Name}
	}
	return out
}
