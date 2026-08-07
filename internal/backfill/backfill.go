// Package backfill implements REQUIREMENTS.md FR-USAGE-2: indexing
// ~/.claude/projects/**/*.jsonl — Claude Code's own per-session transcript
// files, already on disk — into internal/budget's usage ledger, so the
// user's interactive sessions (not just tokenwarden's own dispatches)
// contribute to usage totals from day one. Depends only on internal/budget
// (for the Ledger it writes into) and internal/store (indirectly, via
// budget) — never on internal/queue, internal/runner, or internal/dispatch,
// since this has nothing to do with dispatching work.
package backfill

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"tokenwarden/internal/budget"
)

// maxLineSize bounds a single transcript line's size. Transcript lines can
// be large (tool output, file contents) but not unbounded; this matches
// the same defensive pattern internal/runner uses for stream-json lines.
const maxLineSize = 20 * 1024 * 1024

// Stats summarizes one indexing run, for logging.
type Stats struct {
	FilesScanned    int
	LinesScanned    int
	EntriesInserted int
	EntriesSkipped  int // already indexed (idempotent no-op) or unparseable/irrelevant lines
}

// transcriptLine is the subset of a transcript JSONL line this package
// cares about — only "assistant" type lines with token usage are used;
// every other type (user, ai-title, queue-operation, attachment,
// last-prompt, summary, ...) is skipped. Unknown fields are ignored by
// encoding/json automatically, which is what makes this tolerant of the
// transcript format gaining fields over time.
type transcriptLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	UUID      string `json:"uuid"`
	Message   struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// DefaultProjectsDir returns Claude Code's transcript directory,
// ~/.claude/projects — the same location REQUIREMENTS.md FR-USAGE-2 names.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// IndexProjects walks projectsDir for *.jsonl transcript files and records
// each assistant-turn's token usage into ledger, keyed by that line's uuid
// so re-running this (e.g. on every daemon startup) is idempotent — already
// -indexed lines are silently skipped, not double-counted. A missing
// projectsDir (Claude Code never run, or a fresh machine) is not an error:
// Stats comes back zeroed. Errors reading or parsing individual files/lines
// don't abort the whole walk — this is best-effort historical enrichment,
// not a critical path; only directory-level walk failures return an error.
func IndexProjects(ctx context.Context, ledger *budget.Ledger, projectsDir string) (Stats, error) {
	var stats Stats

	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return stats, nil
	}

	err := filepath.WalkDir(projectsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip files/dirs we can't stat, keep walking
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}

		stats.FilesScanned++
		fileStats, indexErr := indexFile(ctx, ledger, path)
		if indexErr != nil {
			return nil // one bad file shouldn't abort the whole scan
		}
		stats.LinesScanned += fileStats.LinesScanned
		stats.EntriesInserted += fileStats.EntriesInserted
		stats.EntriesSkipped += fileStats.EntriesSkipped
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("walking %s: %w", projectsDir, err)
	}
	return stats, nil
}

func indexFile(ctx context.Context, ledger *budget.Ledger, path string) (Stats, error) {
	var stats Stats

	f, err := os.Open(path)
	if err != nil {
		return stats, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	for scanner.Scan() {
		stats.LinesScanned++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var tl transcriptLine
		if err := json.Unmarshal(line, &tl); err != nil {
			stats.EntriesSkipped++
			continue
		}
		if tl.Type != "assistant" || tl.UUID == "" {
			stats.EntriesSkipped++
			continue
		}
		u := tl.Message.Usage
		if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0 {
			stats.EntriesSkipped++
			continue
		}

		recordedAt := time.Now().UTC()
		if ts, err := time.Parse(time.RFC3339Nano, tl.Timestamp); err == nil {
			recordedAt = ts
		}

		jobID := tl.SessionID
		if jobID != "" {
			jobID = "interactive:" + jobID
		}

		inserted, err := ledger.RecordHistoricalUsage(ctx, budget.HistoricalUsage{
			JobID:                    jobID,
			Model:                    tl.Message.Model,
			InputTokens:              u.InputTokens,
			OutputTokens:             u.OutputTokens,
			CacheCreationInputTokens: u.CacheCreationInputTokens,
			CacheReadInputTokens:     u.CacheReadInputTokens,
			RecordedAt:               recordedAt,
			SourceUUID:               tl.UUID,
		})
		if err != nil {
			stats.EntriesSkipped++
			continue
		}
		if inserted {
			stats.EntriesInserted++
		} else {
			stats.EntriesSkipped++
		}
	}

	return stats, scanner.Err()
}
