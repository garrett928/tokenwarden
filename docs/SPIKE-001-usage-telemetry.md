# SPIKE-001 — Which channel exposes Claude plan-limit state?

**Date:** 2026-08-01 · **Claude Code version:** 2.1.152 · **Platform:** macOS (darwin/arm64) · **Account:** Claude Pro/Max subscription

## Question

tokenwarden's pacing engine needs live 5-hour and 7-day window fill. The `statusLine` JSON payload is the only documented machine-readable source. **Does it fire when Claude Code runs headlessly under `-p`?** If not, what is the fallback?

This determines the entire architecture, so it was settled before any code was written.

---

## Experiment 1 — `statusLine` under `claude -p`

A probe script that writes its stdin to a file, injected via `--settings`:

```bash
claude -p "Reply with exactly: pong" \
  --model haiku \
  --settings '{"statusLine":{"type":"command","command":"/…/probe.sh"}}' \
  --output-format json
```

**Result: the probe never ran.** Exit code 0, valid response, empty stderr, no payload file.

### Conclusion 1

> **`statusLine` does not fire in headless (`-p`) mode.** Dispatched jobs cannot self-report plan-limit state.

This is consistent with the documentation describing the status line as a rendered UI element — there is no status bar to render in print mode.

---

## Experiment 2 — What `-p` *does* return

The same run's `--output-format json` result:

```json
{
  "type": "result", "subtype": "success",
  "duration_ms": 2728, "duration_api_ms": 3529, "ttft_ms": 1601,
  "num_turns": 1, "result": "pong", "stop_reason": "end_turn",
  "session_id": "538a1cff-…", "total_cost_usd": 0.0230845,
  "usage": {
    "input_tokens": 10,
    "cache_creation_input_tokens": 17878,
    "cache_read_input_tokens": 0,
    "output_tokens": 43,
    "cache_creation": {
      "ephemeral_1h_input_tokens": 17878,
      "ephemeral_5m_input_tokens": 0
    },
    "service_tier": "standard", "speed": "standard"
  },
  "modelUsage": {
    "claude-haiku-4-5-20251001": {
      "inputTokens": 452, "outputTokens": 57,
      "cacheReadInputTokens": 0, "cacheCreationInputTokens": 17878,
      "costUSD": 0.0230845, "contextWindow": 200000
    }
  },
  "permission_denials": [], "terminal_reason": "completed"
}
```

### Conclusion 2

> Headless runs give **exact ledger data** — per-model tokens, cost, and a cache-creation split by TTL (1h vs 5m) that is directly useful for the cache-miss insight (FR-USAGE-8). They give **nothing** about window fill.

Note the shape of the cost: a trivial "say pong" run still cost **$0.023**, essentially all of it the 17,878-token cache-creation write for the system prompt. This is the floor price of any session, and it sets the cost of the PTY sentinel in Experiment 3.

---

## Experiment 3 — `statusLine` in a PTY-attached interactive session

Driven from Python via `pty.fork()`, with a probe writing each render to a timestamped file:

```python
pid, fd = pty.fork()
if pid == 0:
    os.execvp("claude", ["claude", "--model", "haiku",
                         "--settings", '{"statusLine":{...}}'])
os.write(fd, b"say pong\r")
# …collect renders until one contains rate_limits…
```

**Result: 2 renders captured.**

- **Render 1** (session start, before any API response) — keys present: `context_window`, `cost`, `cwd`, `exceeds_200k_tokens`, `fast_mode`, `model`, `output_style`, `session_id`, `thinking`, `transcript_path`, `version`, `workspace`. **`rate_limits` absent.**
- **Render 2** (after the first API response):

```json
{
  "five_hour": { "used_percentage": 89, "resets_at": 1785642600 },
  "seven_day": { "used_percentage": 22, "resets_at": 1786104000 }
}
```

### Conclusion 3

> **A PTY-attached interactive session is a working ground-truth channel.** `rate_limits` appears on the render *following* the first API response, not on the initial render — a consumer that reads only the first payload will always miss it.

The PTY sentinel is therefore **validated**, not speculative. Cost per fix ≈ $0.02 (Experiment 2's floor price), which is negligible against a weekly budget when rate-limited to a sensible cadence.

---

## Conclusion 4 — `used_percentage` is an integer

Observed: `89` and `22`. The [official documentation example](https://code.claude.com/docs/en/statusline) shows `23.5`, implying a float.

> **Treat resolution as 1%.** On a Max 20× plan a single percent is a meaningful amount of work, so no single reading can be treated as precise.

Consequences, carried into REQUIREMENTS.md §6.3:

- Calibration must fit across many observations, never a single delta.
- Safety margin ≥ 2× quantisation error (≥2% headroom below any ceiling).
- Estimates carry an explicit uncertainty band, and the UI shows the band rather than a false-precision number.

---

## Resulting architecture

| Need | Channel |
|---|---|
| Execute jobs | `claude -p --output-format stream-json` |
| Exact spend per run | `result` message `usage` + `modelUsage` |
| Historical spend | `~/.claude/projects/**/*.jsonl` index |
| Attribution (skill/subagent/MCP) | optional OTel export to a local receiver |
| **Plan-limit ground truth** | `twprobe` statusline shim, via **(a)** global install in `~/.claude/settings.json` capturing the user's own interactive sessions for free, and **(b)** a periodic PTY sentinel for unattended stretches |
| Between fixes | ledger + learned calibration, with decaying confidence |
| Hard stop | `api_retry` event with `error: "rate_limit"` |

## Follow-ups

- Measure the accuracy of dead reckoning between sentinel fixes overnight; use it to set sentinel cadence (Open Question 1).
- Confirm whether the reported Max-plan model-specific weekly cap is visible anywhere in `rate_limits`, or only discoverable via an error (Open Question 4).

## Reproducing

Scripts live in `internal/budget/testdata/spike/`. They spend roughly $0.05 total. The captured payloads are checked in as test fixtures so the parsers can be tested without spending anything.
