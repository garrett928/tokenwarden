package logging

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetup_WritesToFileAndStderr_IncludingStdlibLog(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	path := filepath.Join(t.TempDir(), "sub", "tokenwardend.log")
	var stderr bytes.Buffer
	closeFn, err := Setup(path, &stderr)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	slog.Info("hello", "k", "v")
	log.Printf("legacy line %d", 7)
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"msg=hello", "k=v", `msg="legacy line 7"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log file missing %q:\n%s", want, data)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestSetup_EmptyPath_StderrOnly(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var stderr bytes.Buffer
	if _, err := Setup("", &stderr); err != nil {
		t.Fatal(err)
	}
	slog.Info("only stderr")
	if !strings.Contains(stderr.String(), "only stderr") {
		t.Errorf("stderr = %q, want the line", stderr.String())
	}
}
