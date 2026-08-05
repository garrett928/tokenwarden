// Command fakeclaude stands in for the real `claude` binary in tests
// (CLAUDE.md, NFR-TEST-1): it emits scripted stream-json fixtures instead
// of doing anything real, so internal/runner's integration tests spend
// zero tokens and touch no network. It is never part of `go build ./...`
// for the main module — testdata directories are excluded by convention —
// and is only ever compiled explicitly by internal/runner's TestMain.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed fixtures/*.jsonl fixtures/version.txt
var fixturesFS embed.FS

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--version" {
		data, err := fixturesFS.ReadFile("fixtures/version.txt")
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeclaude: missing version fixture:", err)
			os.Exit(2)
		}
		fmt.Print(string(data))
		return
	}

	fixture := os.Getenv("FAKECLAUDE_FIXTURE")
	if fixture == "" {
		fmt.Fprintln(os.Stderr, "fakeclaude: FAKECLAUDE_FIXTURE not set")
		os.Exit(2)
	}

	// echoargs is not a fixture file — it dumps the argv fakeclaude actually
	// received as a deliberately-unrecognized event type ("debug_argv"), so
	// a test can both assert real subprocess argv matches what BuildArgs
	// computed in isolation, and exercise the UnknownEvent forward-compat
	// path on a real, non-fixture event.
	if fixture == "echoargs" {
		payload := map[string]any{"type": "debug_argv", "argv": os.Args[1:]}
		b, err := json.Marshal(payload)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeclaude: encoding argv:", err)
			os.Exit(2)
		}
		fmt.Println(string(b))
		return
	}

	// FAKECLAUDE_DELAY_MS lets a test simulate a long-running dispatch: the
	// process sleeps before emitting anything, so a short context timeout
	// deterministically kills it before any output — exercising Run's
	// context-cancellation path without a real slow subprocess.
	if delayStr := os.Getenv("FAKECLAUDE_DELAY_MS"); delayStr != "" {
		if ms, err := strconv.Atoi(delayStr); err == nil {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}

	data, err := fixturesFS.ReadFile("fixtures/" + fixture + ".jsonl")
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude: unknown fixture", fixture, ":", err)
		os.Exit(2)
	}

	exitCode := 0
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "#exit:"); ok {
			if code, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				exitCode = code
			}
			continue
		}
		fmt.Println(line)
	}
	os.Exit(exitCode)
}
