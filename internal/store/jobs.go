package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateJob validates and persists a new job. It assigns an ID if one isn't
// set, defaults Status to Queued if unset, and stamps CreatedAt/UpdatedAt.
// The passed-in job is not mutated; the persisted copy (with generated
// fields filled in) is returned.
func (s *Store) CreateJob(ctx context.Context, j Job) (Job, error) {
	if err := validateNewJob(j); err != nil {
		return Job{}, err
	}

	if j.ID == "" {
		id, err := newJobID()
		if err != nil {
			return Job{}, err
		}
		j.ID = id
	}
	if j.Status == "" {
		j.Status = StatusQueued
	}
	now := time.Now().UTC()
	j.CreatedAt = now
	j.UpdatedAt = now

	// Normalize nil slices to empty before returning j, so the caller sees
	// the same shape CreateJob gives back as a later GetJob would (which
	// round-trips through JSON and never produces nil) — a nil-vs-empty
	// mismatch between "just created" and "fetched" is exactly the kind of
	// inconsistency that shows up as a confusing API response.
	j.Attachments = nonNil(j.Attachments)
	j.Steps = nonNilStrings(j.Steps)
	j.DependsOn = nonNilStrings(j.DependsOn)
	j.AllowedTools = nonNilStrings(j.AllowedTools)
	j.AddDirs = nonNilStrings(j.AddDirs)

	attachmentsJSON, err := json.Marshal(j.Attachments)
	if err != nil {
		return Job{}, fmt.Errorf("encoding attachments: %w", err)
	}
	stepsJSON, err := json.Marshal(j.Steps)
	if err != nil {
		return Job{}, fmt.Errorf("encoding steps: %w", err)
	}
	dependsOnJSON, err := json.Marshal(j.DependsOn)
	if err != nil {
		return Job{}, fmt.Errorf("encoding depends_on: %w", err)
	}
	allowedToolsJSON, err := json.Marshal(j.AllowedTools)
	if err != nil {
		return Job{}, fmt.Errorf("encoding allowed_tools: %w", err)
	}
	addDirsJSON, err := json.Marshal(j.AddDirs)
	if err != nil {
		return Job{}, fmt.Errorf("encoding add_dirs: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO jobs (
			id, kind, prompt, workspace, model, effort,
			attachments, steps, resumable, priority,
			earliest_at, deadline_at, max_budget_usd, depends_on,
			session_id, status, failure_reason, result_text, created_at, updated_at,
			permission_mode, allowed_tools, add_dirs, json_schema, freeform_worktree
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		j.ID, string(j.Kind), j.Prompt, j.Workspace, j.Model, j.Effort,
		string(attachmentsJSON), string(stepsJSON), j.Resumable, j.Priority,
		unixOrNil(j.EarliestAt), unixOrNil(j.DeadlineAt), j.MaxBudgetUSD, string(dependsOnJSON),
		j.SessionID, string(j.Status), j.FailureReason, j.Result, j.CreatedAt.Unix(), j.UpdatedAt.Unix(),
		j.PermissionMode, string(allowedToolsJSON), string(addDirsJSON), j.JSONSchema, j.FreeformWorktree,
	)
	if err != nil {
		return Job{}, fmt.Errorf("inserting job: %w", err)
	}

	return j, nil
}

// GetJob fetches a single job by ID, or ErrNotFound if it doesn't exist.
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	row := s.db.QueryRowContext(ctx, jobSelectColumns+` FROM jobs WHERE id = ?`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("getting job %s: %w", id, err)
	}
	return j, nil
}

// GetJobs fetches multiple jobs by ID in one query. Missing IDs are simply
// absent from the result — callers that need to distinguish "not found"
// per-ID should check len(result) against len(ids).
func (s *Store) GetJobs(ctx context.Context, ids []string) ([]Job, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := jobSelectColumns + ` FROM jobs WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("getting jobs: %w", err)
	}
	defer rows.Close()

	return scanJobs(rows)
}

// ListJobs returns jobs matching filter, ordered by priority (highest
// first) and then by creation time (oldest first) as a tiebreaker.
func (s *Store) ListJobs(ctx context.Context, filter ListFilter) ([]Job, error) {
	query := jobSelectColumns + ` FROM jobs`
	var args []any

	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, st := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, string(st))
		}
		query += ` WHERE status IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY priority DESC, created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	defer rows.Close()

	return scanJobs(rows)
}

// UpdateStatus transitions a job to a new status, optionally recording a
// failure reason (pass "" when not applicable). Returns ErrNotFound if the
// job doesn't exist.
func (s *Store) UpdateStatus(ctx context.Context, id string, status Status, failureReason string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = ?, failure_reason = ?, updated_at = ? WHERE id = ?
	`, string(status), failureReason, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("updating status for job %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// UpdateSessionID records the Claude session ID a job's first dispatch
// produced, so a later --resume can continue it.
func (s *Store) UpdateSessionID(ctx context.Context, id, sessionID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET session_id = ?, updated_at = ? WHERE id = ?
	`, sessionID, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("updating session id for job %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// UpdateJobMaxBudgetUSD sets a job's per-dispatch spend cap — used both at
// job creation (via CreateJob) and by the scheduler's §6.4 strategy 1
// (budget-capped continuation), which caps a resumable job's next dispatch
// to remaining 5-hour headroom rather than letting it overshoot.
func (s *Store) UpdateJobMaxBudgetUSD(ctx context.Context, id string, maxBudgetUSD *float64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET max_budget_usd = ?, updated_at = ? WHERE id = ?
	`, maxBudgetUSD, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("updating max budget for job %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// UpdateResult records the runner's final text output for a job — see
// Job.Result's doc comment for why this is set on both success and failure.
func (s *Store) UpdateResult(ctx context.Context, id, resultText string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET result_text = ?, updated_at = ? WHERE id = ?
	`, resultText, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("updating result for job %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// DeleteJob permanently removes a job. Most callers should prefer
// transitioning to StatusCancelled via UpdateStatus — this exists for
// cleanup and tests, not as the everyday "cancel" path.
func (s *Store) DeleteJob(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting job %s: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

func checkRowsAffected(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for job %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func validateNewJob(j Job) error {
	if strings.TrimSpace(j.Prompt) == "" {
		return fmt.Errorf("%w: prompt is required", ErrInvalidJob)
	}
	if j.Kind == "" {
		return fmt.Errorf("%w: kind is required", ErrInvalidJob)
	}
	if !j.Kind.valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidJob, j.Kind)
	}
	if j.Status != "" && !isKnownStatus(j.Status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidJob, j.Status)
	}
	return nil
}

var ErrInvalidJob = errors.New("invalid job")

func isKnownStatus(s Status) bool {
	switch s {
	case StatusQueued, StatusBlocked, StatusRunning, StatusPausedBudget,
		StatusDeferredOversized, StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func nonNil(a []Attachment) []Attachment {
	if a == nil {
		return []Attachment{}
	}
	return a
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
