package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"tokenwarden/internal/api"
	"tokenwarden/internal/cliclient"
	"tokenwarden/internal/config"
	"tokenwarden/internal/store"
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
	printCalibration("5h", u.FiveHourCalibration)
	printCalibration("7d", u.SevenDayCalibration)
	return nil
}

func cmdKillSwitchHalt(args []string) error {
	fs := flag.NewFlagSet("kill-switch halt", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	resp, err := newClient().HaltDispatch(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Kill switch: halted = %v\n", resp.Halted)
	return nil
}

func cmdKillSwitchResume(args []string) error {
	fs := flag.NewFlagSet("kill-switch resume", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	resp, err := newClient().ResumeDispatch(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Kill switch: halted = %v\n", resp.Halted)
	return nil
}

func cmdKillSwitchStatus(args []string) error {
	fs := flag.NewFlagSet("kill-switch status", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	resp, err := newClient().KillSwitchStatus(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Kill switch: halted = %v\n", resp.Halted)
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

func printCalibration(label string, c api.CalibrationResponse) {
	if c.Insufficient {
		fmt.Printf("Calibration (%s): insufficient data yet (%d sample pair(s), need at least 3)\n", label, c.Samples)
		return
	}
	fmt.Printf("Calibration (%s): ~%.0f tokens per 1%% (from %d sample pairs)\n", label, c.TokensPerPercent, c.Samples)
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
	if j.Result != "" {
		fmt.Printf("Result:\n  %s\n", j.Result)
	}
}

// parseTimeBlock parses a time block in format [Days:]HH:MM-HH:MM.
// Days is optional, comma-separated three-letter weekday abbreviations (case-insensitive).
// HH:MM-HH:MM is 24-hour time; EndMin <= StartMin means wraparound past midnight.
func parseTimeBlock(s string) (store.TimeBlock, error) {
	// Try parsing as pure time range first (no day prefix).
	if parts := strings.Split(s, "-"); len(parts) == 2 {
		// Both parts should look like HH:MM. If the first part also contains ':'
		// and might be a day list, we need to disambiguate.
		firstPart := parts[0]
		if colonIdx := strings.LastIndex(firstPart, ":"); colonIdx > 0 {
			// There's at least one ':' in the first part. Check if it's just HH:MM
			// by trying to parse HH:MM.
			before := firstPart[:colonIdx]
			after := firstPart[colonIdx+1:]
			_, errBefore := strconv.Atoi(before)
			_, errAfter := strconv.Atoi(after)
			if errBefore == nil && errAfter == nil && len(before) <= 2 && len(after) == 2 {
				// Looks like HH:MM with no day prefix, parse as pure time range.
				return parseTimeRange(s)
			}
			// Otherwise, split on the first ':' to separate days from time range.
		}
		// Try as pure time range anyway.
		tb, err := parseTimeRange(s)
		if err == nil {
			return tb, nil
		}
	}

	// Split on first ':' to separate day list from time range.
	colonIdx := strings.Index(s, ":")
	if colonIdx < 0 {
		return store.TimeBlock{}, fmt.Errorf("invalid time block %q: no colon found", s)
	}

	dayStr := s[:colonIdx]
	timeStr := s[colonIdx+1:]

	// Parse day list.
	days, err := parseDayList(dayStr)
	if err != nil {
		return store.TimeBlock{}, fmt.Errorf("invalid time block %q: %w", s, err)
	}

	// Parse time range.
	timeBlock, err := parseTimeRange(timeStr)
	if err != nil {
		return store.TimeBlock{}, fmt.Errorf("invalid time block %q: %w", s, err)
	}

	timeBlock.Days = days
	return timeBlock, nil
}

// parseTimeRange parses HH:MM-HH:MM into start and end minutes since midnight.
func parseTimeRange(s string) (store.TimeBlock, error) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return store.TimeBlock{}, fmt.Errorf("time range must be HH:MM-HH:MM")
	}

	start, err := parseTime(strings.TrimSpace(parts[0]))
	if err != nil {
		return store.TimeBlock{}, err
	}

	end, err := parseTime(strings.TrimSpace(parts[1]))
	if err != nil {
		return store.TimeBlock{}, err
	}

	if start < 0 || start >= 1440 || end < 0 || end >= 1440 {
		return store.TimeBlock{}, fmt.Errorf("minutes must be in [0, 1440)")
	}

	return store.TimeBlock{StartMin: start, EndMin: end}, nil
}

// parseTime parses HH:MM into minutes since midnight.
func parseTime(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: %w", s, err)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// parseDayList parses comma-separated three-letter weekday abbreviations.
func parseDayList(s string) ([]time.Weekday, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil // empty = every day
	}

	dayMap := map[string]time.Weekday{
		"sun": time.Sunday,
		"mon": time.Monday,
		"tue": time.Tuesday,
		"wed": time.Wednesday,
		"thu": time.Thursday,
		"fri": time.Friday,
		"sat": time.Saturday,
	}

	parts := strings.Split(s, ",")
	var days []time.Weekday
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		day, ok := dayMap[p]
		if !ok {
			return nil, fmt.Errorf("unknown weekday abbreviation %q", p)
		}
		days = append(days, day)
	}
	return days, nil
}

// formatTimeBlock formats a TimeBlockDTO into [Days:]HH:MM-HH:MM format.
// Output can be parsed back with parseTimeBlock.
func formatTimeBlock(b api.TimeBlockDTO) string {
	startH := b.StartMin / 60
	startM := b.StartMin % 60
	endH := b.EndMin / 60
	endM := b.EndMin % 60

	timeStr := fmt.Sprintf("%02d:%02d-%02d:%02d", startH, startM, endH, endM)

	if len(b.Days) == 0 {
		return timeStr
	}

	dayNames := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	var dayStrs []string
	for _, d := range b.Days {
		dayStrs = append(dayStrs, dayNames[d])
	}
	return fmt.Sprintf("%s:%s", strings.Join(dayStrs, ","), timeStr)
}

// timeBlockListValue is a flag.Value for repeatable time block flags.
type timeBlockListValue struct {
	blocks []store.TimeBlock
}

func (v *timeBlockListValue) String() string {
	if len(v.blocks) == 0 {
		return ""
	}
	var strs []string
	for _, b := range v.blocks {
		strs = append(strs, formatTimeBlock(storeBlockToDTO(b)))
	}
	return strings.Join(strs, ";")
}

func (v *timeBlockListValue) Set(s string) error {
	block, err := parseTimeBlock(s)
	if err != nil {
		return err
	}
	v.blocks = append(v.blocks, block)
	return nil
}

func cmdSchedulerConfigShow(args []string) error {
	fs := flag.NewFlagSet("scheduler config show", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	cfg, err := newClient().GetSchedulerConfig(ctx)
	if err != nil {
		return err
	}
	printSchedulerConfig(cfg)
	return nil
}

func cmdSchedulerConfigSet(args []string) error {
	fs := flag.NewFlagSet("scheduler config set", flag.ExitOnError)
	enabled := fs.Bool("enabled", false, "enable the scheduler")
	disabled := fs.Bool("disabled", false, "disable the scheduler")
	aggressiveness := fs.Int("aggressiveness", -1, "0-100: how much of weekly capacity to target, and how full to let the 5h window get (FR-SCHED-1); -1 leaves unchanged")
	maxBudget := fs.Float64("max-budget-usd", -1, "global safety cap in USD; negative leaves unchanged")
	clearMaxBudget := fs.Bool("clear-max-budget-usd", false, "remove the global safety cap")
	clearReservedBlocks := fs.Bool("clear-reserved-blocks", false, "clear all reserved blocks")
	clearPreferredWindows := fs.Bool("clear-preferred-windows", false, "clear all preferred windows")

	var reservedBlocks, preferredWindows timeBlockListValue
	fs.Var(&reservedBlocks, "reserved-block", "repeatable: add a reserved block in format [Days:]HH:MM-HH:MM")
	fs.Var(&preferredWindows, "preferred-window", "repeatable: add a preferred window in format [Days:]HH:MM-HH:MM")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *enabled && *disabled {
		return fmt.Errorf("--enabled and --disabled are mutually exclusive")
	}
	if len(reservedBlocks.blocks) > 0 && *clearReservedBlocks {
		return fmt.Errorf("--reserved-block and --clear-reserved-blocks are mutually exclusive")
	}
	if len(preferredWindows.blocks) > 0 && *clearPreferredWindows {
		return fmt.Errorf("--preferred-window and --clear-preferred-windows are mutually exclusive")
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	client := newClient()

	// Fetch the current config first so a partial update doesn't wipe fields,
	// since PUT replaces the whole config.
	current, err := client.GetSchedulerConfig(ctx)
	if err != nil {
		return err
	}

	req := api.UpdateSchedulerConfigRequest{
		Enabled:          current.Enabled,
		Aggressiveness:   current.Aggressiveness,
		ReservedBlocks:   current.ReservedBlocks,
		PreferredWindows: current.PreferredWindows,
		MaxBudgetUSD:     current.MaxBudgetUSD,
	}
	if *enabled {
		req.Enabled = true
	}
	if *disabled {
		req.Enabled = false
	}
	if *aggressiveness >= 0 {
		req.Aggressiveness = *aggressiveness
	}
	if *clearMaxBudget {
		req.MaxBudgetUSD = nil
	} else if *maxBudget >= 0 {
		req.MaxBudgetUSD = maxBudget
	}

	// Handle reserved blocks.
	if *clearReservedBlocks {
		req.ReservedBlocks = nil
	} else if len(reservedBlocks.blocks) > 0 {
		req.ReservedBlocks = storeBlocksToDTO(reservedBlocks.blocks)
	}

	// Handle preferred windows.
	if *clearPreferredWindows {
		req.PreferredWindows = nil
	} else if len(preferredWindows.blocks) > 0 {
		req.PreferredWindows = storeBlocksToDTO(preferredWindows.blocks)
	}

	updated, err := client.UpdateSchedulerConfig(ctx, req)
	if err != nil {
		return err
	}
	printSchedulerConfig(updated)
	return nil
}

// storeBlockToDTO converts a single store.TimeBlock to an api.TimeBlockDTO.
func storeBlockToDTO(b store.TimeBlock) api.TimeBlockDTO {
	dto := api.TimeBlockDTO{StartMin: b.StartMin, EndMin: b.EndMin}
	for _, d := range b.Days {
		dto.Days = append(dto.Days, int(d))
	}
	return dto
}

// storeBlocksToDTO converts store.TimeBlock to api.TimeBlockDTO.
func storeBlocksToDTO(blocks []store.TimeBlock) []api.TimeBlockDTO {
	var dtos []api.TimeBlockDTO
	for _, b := range blocks {
		dtos = append(dtos, storeBlockToDTO(b))
	}
	return dtos
}

func printSchedulerConfig(cfg api.SchedulerConfigResponse) {
	fmt.Printf("Enabled:           %v\n", cfg.Enabled)
	fmt.Printf("Aggressiveness:    %d%%\n", cfg.Aggressiveness)
	if cfg.MaxBudgetUSD != nil {
		fmt.Printf("Max budget:        $%.2f\n", *cfg.MaxBudgetUSD)
	} else {
		fmt.Println("Max budget:        (none)")
	}

	if len(cfg.ReservedBlocks) == 0 {
		fmt.Println("Reserved blocks:   (none)")
	} else {
		fmt.Println("Reserved blocks:")
		for _, b := range cfg.ReservedBlocks {
			formatted := formatTimeBlock(b)
			if len(b.Days) == 0 {
				formatted += " (every day)"
			}
			fmt.Printf("  %s\n", formatted)
		}
	}

	if len(cfg.PreferredWindows) == 0 {
		fmt.Println("Preferred windows: (none)")
	} else {
		fmt.Println("Preferred windows:")
		for _, w := range cfg.PreferredWindows {
			formatted := formatTimeBlock(w)
			if len(w.Days) == 0 {
				formatted += " (every day)"
			}
			fmt.Printf("  %s\n", formatted)
		}
	}
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
