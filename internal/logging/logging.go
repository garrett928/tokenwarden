// Package logging wires the daemon's process-wide logger: a log/slog text
// handler (key=value lines) writing to both stderr and a persistent log
// file, installed as slog's default. The standard library's log package is
// routed through the same handler by slog.SetDefault, so pre-existing
// log.Printf call sites land in the file in the same format (as msg=...).
//
// The file exists so an unattended run (e.g. overnight) can be judged
// after the fact from the log alone — see the scheduler and dispatch
// packages for what gets logged.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Setup opens path for appending (creating it and any parent directories),
// installs a slog default that writes to both stderr and that file, and
// returns a function that closes the file. An empty path logs to stderr
// only.
func Setup(path string, stderr io.Writer) (closeFn func() error, err error) {
	w := stderr
	closeFn = func() error { return nil }
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("creating log dir for %s: %w", path, err)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening log file %s: %w", path, err)
		}
		w = io.MultiWriter(stderr, f)
		closeFn = f.Close
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, nil)))
	return closeFn, nil
}
