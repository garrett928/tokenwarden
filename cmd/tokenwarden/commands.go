package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"tokenwarden/internal/api"
	"tokenwarden/internal/cliclient"
	"tokenwarden/internal/config"
)

const requestTimeout = 10 * time.Second

func baseURL() string {
	if v := os.Getenv("TOKENWARDEN_ADDR"); v != "" {
		return v
	}
	return "http://" + config.DefaultListenAddr
}

func newClient() *cliclient.Client {
	return cliclient.New(baseURL())
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := newClient().Health(ctx); err != nil {
		return fmt.Errorf("daemon at %s is not reachable: %w", baseURL(), err)
	}
	fmt.Printf("tokenwardend at %s: ok\n", baseURL())
	return nil
}

func cmdQueueAdd(args []string) error {
	fs := flag.NewFlagSet("queue add", flag.ExitOnError)
	kind := fs.String("kind", "", "job kind: research, plan, code, review, freeform (required)")
	prompt := fs.String("prompt", "", "the prompt text (required)")
	workspace := fs.String("workspace", "", "workspace directory the job runs in")
	model := fs.String("model", "", "model override (e.g. opus, sonnet, haiku)")
	effort := fs.String("effort", "", "thinking effort: low, medium, high, xhigh, max")
	priority := fs.Int("priority", 0, "higher runs first")
	resumable := fs.Bool("resumable", false, "may be budget-capped and resumed later (REQUIREMENTS.md §6.4)")
	maxBudget := fs.Float64("max-budget-usd", 0, "hard per-job spend ceiling in USD (0 = unset)")
	dependsOn := fs.String("depends-on", "", "comma-separated job IDs this job waits on")
	steps := fs.String("steps", "", "comma-separated user-declared split points")
	permissionMode := fs.String("permission-mode", "", "freeform only: plan, dontAsk, acceptEdits, or default")
	allowedTools := fs.String("allowed-tools", "", "freeform only: comma-separated tool allowlist")
	addDirs := fs.String("add-dir", "", "freeform only: comma-separated extra --add-dir paths")
	freeformWorktree := fs.Bool("freeform-worktree", false, "freeform only: run in an isolated --worktree")
	jsonSchema := fs.String("json-schema", "", "structured result schema, primarily for research jobs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *kind == "" || *prompt == "" {
		return fmt.Errorf("--kind and --prompt are required")
	}

	req := api.CreateJobRequest{
		Kind:             *kind,
		Prompt:           *prompt,
		Workspace:        *workspace,
		Model:            *model,
		Effort:           *effort,
		Priority:         *priority,
		Resumable:        *resumable,
		DependsOn:        splitNonEmpty(*dependsOn),
		Steps:            splitNonEmpty(*steps),
		PermissionMode:   *permissionMode,
		AllowedTools:     splitNonEmpty(*allowedTools),
		AddDirs:          splitNonEmpty(*addDirs),
		FreeformWorktree: *freeformWorktree,
		JSONSchema:       *jsonSchema,
	}
	if *maxBudget > 0 {
		req.MaxBudgetUSD = maxBudget
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	job, err := newClient().CreateJob(ctx, req)
	if err != nil {
		return err
	}

	fmt.Printf("queued %s (%s, priority %d, status %s)\n", job.ID, job.Kind, job.Priority, job.Status)
	return nil
}

func cmdQueueList(args []string) error {
	fs := flag.NewFlagSet("queue list", flag.ExitOnError)
	status := fs.String("status", "", "comma-separated statuses to filter by (default: all)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	jobs, err := newClient().ListJobs(ctx, splitNonEmpty(*status))
	if err != nil {
		return err
	}

	if len(jobs) == 0 {
		fmt.Println("no jobs")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tKIND\tSTATUS\tPRIORITY\tPROMPT"); err != nil {
		return err
	}
	for _, j := range jobs {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", j.ID, j.Kind, j.Status, j.Priority, truncate(j.Prompt, 60)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func cmdQueueShow(args []string) error {
	fs := flag.NewFlagSet("queue show", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tokenwarden queue show <job-id>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	job, err := newClient().GetJob(ctx, fs.Arg(0))
	if err != nil {
		return err
	}

	printJob(job)
	return nil
}

func cmdQueueCancel(args []string) error {
	fs := flag.NewFlagSet("queue cancel", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tokenwarden queue cancel <job-id>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	job, err := newClient().CancelJob(ctx, fs.Arg(0))
	if err != nil {
		return err
	}

	fmt.Printf("cancelled %s (status %s)\n", job.ID, job.Status)
	return nil
}

func cmdQueueDispatch(args []string) error {
	fs := flag.NewFlagSet("queue dispatch", flag.ExitOnError)
	wait := fs.Bool("wait", false, "poll until the job reaches a terminal status before returning")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tokenwarden queue dispatch <job-id> [--wait]")
	}
	id := fs.Arg(0)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	job, err := newClient().DispatchJob(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("dispatched %s (status %s)\n", job.ID, job.Status)

	if !*wait {
		return nil
	}

	// A client-side poll loop on a request the CLI itself just made — not
	// server-side automation. The daemon has no background dispatch loop;
	// this just waits politely on the one job it was told to run.
	for {
		time.Sleep(2 * time.Second)
		waitCtx, waitCancel := context.WithTimeout(context.Background(), requestTimeout)
		job, err = newClient().GetJob(waitCtx, id)
		waitCancel()
		if err != nil {
			return err
		}
		if isTerminalStatus(job.Status) {
			fmt.Printf("finished %s (status %s)\n", job.ID, job.Status)
			if job.FailureReason != "" {
				fmt.Printf("reason: %s\n", job.FailureReason)
			}
			return nil
		}
	}
}

func cmdUsage(args []string) error {
	fs := flag.NewFlagSet("usage", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	u, err := newClient().Usage(ctx)
	if err != nil {
		return err
	}

	fmt.Println("(exact local ledger spend — not the plan's rate-limit window fill; see docs/REQUIREMENTS.md §6.1)")
	printWindowUsage("Last 5 hours", u.FiveHour)
	printWindowUsage("Last 7 days", u.SevenDay)
	printGroundTruth(u.GroundTruth)
	return nil
}

func printGroundTruth(gt *api.GroundTruthResponse) {
	if gt == nil {
		fmt.Println("Ground truth: none yet — install the statusline probe with 'tokenwarden probe install' and use Claude Code interactively at least once")
		return
	}
	fmt.Printf("Ground truth (authoritative, as of %s, %ds ago):\n", gt.ObservedAt.Format(time.RFC3339), gt.AgeSeconds)
	fmt.Printf("  5h window:  %d%% used, resets %s\n", gt.FiveHour.UsedPercentage, time.Unix(gt.FiveHour.ResetsAt, 0).Format(time.RFC3339))
	fmt.Printf("  7d window:  %d%% used, resets %s\n", gt.SevenDay.UsedPercentage, time.Unix(gt.SevenDay.ResetsAt, 0).Format(time.RFC3339))
}

func printWindowUsage(label string, w api.WindowUsage) {
	fmt.Printf("%s:\n", label)
	fmt.Printf("  cost:          $%.4f\n", w.CostUSD)
	fmt.Printf("  input tokens:  %d\n", w.InputTokens)
	fmt.Printf("  output tokens: %d\n", w.OutputTokens)
	fmt.Printf("  cache create:  %d\n", w.CacheCreationInputTokens)
	fmt.Printf("  cache read:    %d\n", w.CacheReadInputTokens)
	fmt.Printf("  entries:       %d\n", w.EntryCount)
}

func isTerminalStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func printJob(j api.JobResponse) {
	fmt.Printf("ID:          %s\n", j.ID)
	fmt.Printf("Kind:        %s\n", j.Kind)
	fmt.Printf("Status:      %s\n", j.Status)
	fmt.Printf("Priority:    %d\n", j.Priority)
	if j.Model != "" {
		fmt.Printf("Model:       %s\n", j.Model)
	}
	if j.Effort != "" {
		fmt.Printf("Effort:      %s\n", j.Effort)
	}
	if j.Workspace != "" {
		fmt.Printf("Workspace:   %s\n", j.Workspace)
	}
	if j.PermissionMode != "" {
		fmt.Printf("Permission:  %s\n", j.PermissionMode)
	}
	if len(j.DependsOn) > 0 {
		fmt.Printf("Depends on:  %s\n", strings.Join(j.DependsOn, ", "))
	}
	if j.SessionID != "" {
		fmt.Printf("Session:     %s\n", j.SessionID)
	}
	if j.FailureReason != "" {
		fmt.Printf("Failure:     %s\n", j.FailureReason)
	}
	fmt.Printf("Created:     %s\n", j.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:     %s\n", j.UpdatedAt.Format(time.RFC3339))
	fmt.Printf("Prompt:\n  %s\n", j.Prompt)
}

func splitNonEmpty(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
