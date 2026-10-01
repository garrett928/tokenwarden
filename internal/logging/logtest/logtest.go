// Package logtest captures slog output in tests so they can assert that the
// operational log lines an unattended run depends on are actually emitted.
package logtest

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
)

// Buffer is a goroutine-safe bytes.Buffer.
type Buffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything logged so far.
func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Capture replaces slog's default logger with a text handler writing to the
// returned Buffer, restoring the previous default when the test ends. Tests
// using it must not run in parallel (the default logger is process-global).
func Capture(t *testing.T) *Buffer {
	t.Helper()
	prev := slog.Default()
	b := &Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(b, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return b
}
