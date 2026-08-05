package runner

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"tokenwarden/internal/store"
)

// progressInstruction is appended to a resumable job's prompt so a run that
// gets cut off (by --max-budget-usd, or a future scheduler's --resume
// continuation, §6.4) leaves the workspace in a legible state rather than
// mid-thought. This is the guardrail REQUIREMENTS.md §6.4 calls out:
// "continuation-capable jobs get an appended instruction to leave a short
// PROGRESS.md before stopping."
const progressInstruction = "\n\nIf you are stopped before finishing (e.g. you hit a budget or time limit), leave a short PROGRESS.md in the workspace summarizing what's done and what's left, before stopping."

// BuildArgs turns a job into the argv `claude` is invoked with. Its inputs
// are exactly job's fields and nothing else — there is no ExtraArgs escape
// hatch anywhere in store.Job, config.Config, or PermissionProfile, which
// is what makes "never reachable via any config path" a structural
// property of this function rather than a convention callers must respect.
func BuildArgs(job store.Job) ([]string, error) {
	profile, err := profileFor(job)
	if err != nil {
		return nil, err
	}

	prompt := job.Prompt
	if job.Resumable {
		prompt += progressInstruction
	}

	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}

	if job.Model != "" {
		args = append(args, "--model", job.Model)
	}
	if job.Effort != "" {
		args = append(args, "--effort", job.Effort)
	}
	if job.MaxBudgetUSD != nil {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(*job.MaxBudgetUSD, 'f', -1, 64))
	}

	args = append(args, "--permission-mode", profile.Mode)
	if len(profile.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(profile.AllowedTools, ","))
	}

	// FR-SAFE-2: unattended (resumable) jobs get worktree isolation even
	// for kinds that don't otherwise require it, on top of whatever the
	// kind's own profile mandates (code, always).
	if profile.Worktree || job.Resumable {
		args = append(args, "--worktree")
	}

	for _, d := range mergeDirs(profile.AddDirs, attachmentDirs(job.Attachments)) {
		args = append(args, "--add-dir", d)
	}

	if job.JSONSchema != "" {
		args = append(args, "--json-schema", job.JSONSchema)
	}

	if job.SessionID == "" {
		id, err := uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("generating session id: %w", err)
		}
		args = append(args, "--session-id", id.String())
	} else {
		args = append(args, "--resume", job.SessionID)
	}

	if err := assertNoSkipPermissions(args); err != nil {
		return nil, err
	}
	return args, nil
}

// assertNoSkipPermissions is defense in depth: BuildArgs's construction
// above should never be able to produce these, but this closing check
// means a future edit to this file that accidentally reintroduces one
// fails loudly instead of silently shipping.
func assertNoSkipPermissions(args []string) error {
	for _, a := range args {
		if strings.Contains(a, "dangerously-skip-permissions") || strings.Contains(a, "bypassPermissions") {
			return fmt.Errorf("internal error: refusing to run with %q", a)
		}
	}
	return nil
}

// attachmentDirs returns the deduplicated set of directories containing
// each attachment, so Claude Code can read them via --add-dir regardless
// of the job's working directory (FR-JOB-5).
func attachmentDirs(attachments []store.Attachment) []string {
	seen := make(map[string]bool)
	var dirs []string
	for _, a := range attachments {
		if a.Path == "" {
			continue
		}
		dir := filepath.Dir(a.Path)
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// mergeDirs combines two directory lists, deduplicated and sorted, for a
// stable, testable --add-dir ordering.
func mergeDirs(a, b []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, d := range a {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range b {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}
