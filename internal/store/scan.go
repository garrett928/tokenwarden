package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const jobSelectColumns = `SELECT
	id, kind, prompt, workspace, model, effort,
	attachments, steps, resumable, priority,
	earliest_at, deadline_at, max_budget_usd, depends_on,
	session_id, parent_job_id, status, failure_reason, result_text, created_at, updated_at,
	permission_mode, allowed_tools, add_dirs, json_schema, freeform_worktree`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting scanJob
// serve GetJob (single row) and ListJobs/GetJobs (multi-row) alike.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var (
		j                                       Job
		kind, status                            string
		attachmentsJSON, stepsJSON, dependsJSON string
		earliestAt, deadlineAt                  sql.NullInt64
		maxBudgetUSD                            sql.NullFloat64
		createdAtUnix, updatedAtUnix            int64
		allowedToolsJSON, addDirsJSON           string
	)

	if err := row.Scan(
		&j.ID, &kind, &j.Prompt, &j.Workspace, &j.Model, &j.Effort,
		&attachmentsJSON, &stepsJSON, &j.Resumable, &j.Priority,
		&earliestAt, &deadlineAt, &maxBudgetUSD, &dependsJSON,
		&j.SessionID, &j.ParentJobID, &status, &j.FailureReason, &j.Result, &createdAtUnix, &updatedAtUnix,
		&j.PermissionMode, &allowedToolsJSON, &addDirsJSON, &j.JSONSchema, &j.FreeformWorktree,
	); err != nil {
		return Job{}, err
	}

	j.Kind = JobKind(kind)
	j.Status = Status(status)
	j.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	j.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()

	if err := json.Unmarshal([]byte(attachmentsJSON), &j.Attachments); err != nil {
		return Job{}, fmt.Errorf("decoding attachments for job %s: %w", j.ID, err)
	}
	if err := json.Unmarshal([]byte(stepsJSON), &j.Steps); err != nil {
		return Job{}, fmt.Errorf("decoding steps for job %s: %w", j.ID, err)
	}
	if err := json.Unmarshal([]byte(dependsJSON), &j.DependsOn); err != nil {
		return Job{}, fmt.Errorf("decoding depends_on for job %s: %w", j.ID, err)
	}
	if err := json.Unmarshal([]byte(allowedToolsJSON), &j.AllowedTools); err != nil {
		return Job{}, fmt.Errorf("decoding allowed_tools for job %s: %w", j.ID, err)
	}
	if err := json.Unmarshal([]byte(addDirsJSON), &j.AddDirs); err != nil {
		return Job{}, fmt.Errorf("decoding add_dirs for job %s: %w", j.ID, err)
	}

	if earliestAt.Valid {
		t := time.Unix(earliestAt.Int64, 0).UTC()
		j.EarliestAt = &t
	}
	if deadlineAt.Valid {
		t := time.Unix(deadlineAt.Int64, 0).UTC()
		j.DeadlineAt = &t
	}
	if maxBudgetUSD.Valid {
		v := maxBudgetUSD.Float64
		j.MaxBudgetUSD = &v
	}

	return j, nil
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating jobs: %w", err)
	}
	return jobs, nil
}
