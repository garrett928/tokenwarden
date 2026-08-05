package runner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"tokenwarden/internal/store"
)

// maxLineSize bounds a single stream-json line's size; well beyond any
// realistic result payload, but keeps a runaway or malicious line from
// growing memory unbounded.
const maxLineSize = 10 * 1024 * 1024

// Runner executes jobs by spawning the claude binary. One Runner may be
// reused across dispatches — it holds only the resolved binary path and a
// memoized version check, no per-job state.
type Runner struct {
	claudeBinary string

	versionOnce sync.Once
	version     string
	versionErr  error
}

// New builds a Runner that invokes claudeBinary (a bare command name
// resolved via PATH, or an absolute path — see internal/config).
func New(claudeBinary string) *Runner {
	return &Runner{claudeBinary: claudeBinary}
}

// RunOptions configures a single Run call.
type RunOptions struct {
	// OnEvent, if set, is called for every parsed event as it arrives,
	// before Run returns — the seam a future live-streaming feature
	// (FR-ART-2) hangs off. Run does not depend on it being set.
	OnEvent func(Event)
}

// Run dispatches job: builds its argv (BuildArgs), spawns claude, and
// parses its stream-json stdout into a Result. It does not implement any
// retry or backoff policy — a system/api_retry(rate_limit) event is
// recorded onto Result.RateLimitEvents and otherwise ignored, since
// deciding what to do about it is a future scheduler's job, not this
// package's.
//
// Run returns a non-nil error only when it couldn't produce a Result at
// all (a bad job, a subprocess that never starts, ctx cancellation, or a
// process that exits without ever emitting a "result" event). A run that
// claude itself reports as failed still returns (Result{IsError: true},
// nil) — that's data, not a Go error.
func (r *Runner) Run(ctx context.Context, job store.Job, opts RunOptions) (Result, error) {
	args, err := BuildArgs(job)
	if err != nil {
		return Result{}, err
	}

	cmd := exec.CommandContext(ctx, r.claudeBinary, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("wiring claude stdout: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("starting claude: %w", err)
	}

	var result Result
	var haveResult bool

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		event, parseErr := ParseEvent(line)
		if parseErr != nil {
			result.UnparseableLines++
			continue
		}
		if opts.OnEvent != nil {
			opts.OnEvent(event)
		}

		switch e := event.(type) {
		case SystemEvent:
			if e.IsRateLimit() {
				result.RateLimitEvents = append(result.RateLimitEvents, e)
			}
		case ResultEvent:
			rateLimitEvents, unparseable := result.RateLimitEvents, result.UnparseableLines
			result = resultFromEvent(e)
			result.RateLimitEvents = rateLimitEvents
			result.UnparseableLines = unparseable
			haveResult = true
		}
	}

	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return result, fmt.Errorf("claude subprocess: %w", ctx.Err())
	}
	if !haveResult {
		return result, fmt.Errorf("%w (exit: %v): %s", ErrNoResultEvent, waitErr, lastLines(stderrBuf.String(), 20))
	}

	return result, nil
}

// lastLines returns at most n trailing non-empty lines of s, for a
// bounded, readable stderr tail in error messages.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
