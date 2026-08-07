// Command twprobe is the statusline shim that captures ground-truth
// rate_limits from the user's own interactive Claude Code sessions
// (REQUIREMENTS.md §6.1 item 1, passive capture path). Installed as the
// `statusLine` command in ~/.claude/settings.json (see `tokenwarden probe
// install`), it runs on every status line render, so it must never block
// a Claude Code turn (NFR-PERF-3): read stdin, best-effort fire the
// reading at the daemon under a tight timeout, print one line, exit.
//
// It deliberately never fails loudly: a daemon that isn't running, a
// malformed payload, or a network hiccup all just mean "nothing to
// report this render," not an error the user needs to see on every
// prompt.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"tokenwarden/internal/api"
	"tokenwarden/internal/config"
)

const (
	maxStdinBytes = 1 << 20 // 1MB — well beyond any realistic status line payload
	postTimeout   = 300 * time.Millisecond
)

func main() {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes))
	if err != nil {
		os.Exit(0)
	}

	payload, err := parseStatusLine(data)
	if err != nil {
		os.Exit(0)
	}

	fmt.Println(renderStatusLine(payload))

	if req, ok := extractRateLimits(payload); ok {
		postGroundTruth(req)
	}
}

// postGroundTruth is best-effort: any failure is logged to stderr (never
// stdout, which is the rendered status line) and otherwise ignored.
func postGroundTruth(req api.RecordGroundTruthRequest) {
	body, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "twprobe: encoding ground truth:", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), postTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, daemonAddr()+"/api/ground-truth", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "twprobe: building request:", err)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: postTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		// The daemon not running is the common case (tokenwarden isn't
		// installed, or is stopped) — not worth surfacing on every render.
		return
	}
	defer resp.Body.Close()
}

func daemonAddr() string {
	if v := os.Getenv("TOKENWARDEN_ADDR"); v != "" {
		return v
	}
	return "http://" + config.DefaultListenAddr
}
