package backfill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/store"
)

func openTestLedger(t *testing.T) *budget.Ledger {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return budget.New(s)
}

// A real (trimmed) transcript excerpt: one irrelevant "user" line, one
// "assistant" line with usage, one duplicate-uuid "assistant" line (to
// prove idempotency within a single file), and one malformed line (to
// prove a bad line doesn't abort the scan).
const testTranscript = `{"type":"user","sessionId":"sess-1","message":{"role":"user","content":"hi"}}
{"type":"assistant","sessionId":"sess-1","timestamp":"2026-08-07T19:27:19.840Z","uuid":"msg-uuid-1","message":{"model":"claude-sonnet-5","usage":{"input_tokens":2,"cache_creation_input_tokens":9321,"cache_read_input_tokens":40828,"output_tokens":299}}}
{"type":"assistant","sessionId":"sess-1","timestamp":"2026-08-07T19:27:19.840Z","uuid":"msg-uuid-1","message":{"model":"claude-sonnet-5","usage":{"input_tokens":2,"cache_creation_input_tokens":9321,"cache_read_input_tokens":40828,"output_tokens":299}}}
not valid json at all
{"type":"assistant","sessionId":"sess-1","uuid":"msg-uuid-2","message":{"model":"claude-sonnet-5","usage":{"input_tokens":5,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`

func writeTestTranscript(t *testing.T, dir string) {
	t.Helper()
	projectDir := filepath.Join(dir, "-some-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "sess-1.jsonl"), []byte(testTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIndexProjects_IndexesAssistantLinesOnly(t *testing.T) {
	ledger := openTestLedger(t)
	dir := t.TempDir()
	writeTestTranscript(t, dir)

	stats, err := IndexProjects(context.Background(), ledger, dir)
	if err != nil {
		t.Fatalf("IndexProjects() error: %v", err)
	}
	if stats.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1", stats.FilesScanned)
	}
	// Two distinct assistant messages (msg-uuid-1, msg-uuid-2) should be
	// inserted; the duplicate msg-uuid-1 line and the malformed/user lines
	// should not increase EntriesInserted.
	if stats.EntriesInserted != 2 {
		t.Errorf("EntriesInserted = %d, want 2 (one duplicate uuid, one user line, one malformed line should all be skipped)", stats.EntriesInserted)
	}

	totals, err := ledger.FiveHourTotal(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if totals.EntryCount != 2 {
		t.Errorf("ledger EntryCount = %d, want 2", totals.EntryCount)
	}
	if totals.InputTokens != 7 { // 2 + 5
		t.Errorf("ledger InputTokens = %d, want 7", totals.InputTokens)
	}
}

func TestIndexProjects_MissingDirIsNotAnError(t *testing.T) {
	ledger := openTestLedger(t)
	stats, err := IndexProjects(context.Background(), ledger, "/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("IndexProjects() on a missing dir: error = %v, want nil", err)
	}
	if stats.FilesScanned != 0 {
		t.Errorf("FilesScanned = %d, want 0", stats.FilesScanned)
	}
}

func TestIndexProjects_ReindexingIsIdempotent(t *testing.T) {
	ledger := openTestLedger(t)
	dir := t.TempDir()
	writeTestTranscript(t, dir)

	if _, err := IndexProjects(context.Background(), ledger, dir); err != nil {
		t.Fatal(err)
	}
	stats2, err := IndexProjects(context.Background(), ledger, dir)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.EntriesInserted != 0 {
		t.Errorf("second IndexProjects() EntriesInserted = %d, want 0 (already indexed)", stats2.EntriesInserted)
	}

	totals, err := ledger.FiveHourTotal(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if totals.EntryCount != 2 {
		t.Errorf("ledger EntryCount after re-indexing = %d, want 2 (no duplicates)", totals.EntryCount)
	}
}
