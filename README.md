# tokenwarden

## Note to readers

This repo is intentionally AI driven. For me, solving the problem below does not warrant enough effort from me to develop this application with lots of human guidance.
To me, this is a great use case of "vibe" coding a highly specific, customized software solution to meet my specific needs. There are software projects that I do
want to put lots of human effort into to learn, understand, and maintain a good project. but this is not one of them. This project simply aims to help me use the tokens I've already paid for
to help research things for me or work on other quality of life projects that would be nice to have but don't warrant enough effort to develop myself.


**Stop wasting the Claude subscription capacity you already paid for.**

A Claude Pro/Max subscription is metered by two rolling windows — a 5-hour session window and a 7-day weekly window. Unused capacity in either **does not roll over**. It refills while you sleep, and expires unused not because there is no work to do, but because nobody was at the keyboard to dispatch it.

tokenwarden is a cross-platform desktop app that holds a queue of real work and paces it against your own Claude subscription, so weekly capacity lands as close to fully used as you choose — while reserving headroom for the hours you want Claude for yourself.

> **Status: pre-alpha.** Requirements and architecture are settled and the foundational spike is complete. Implementation has not started. See [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md).

---

## What it does

- **Queue prompts and tasks** with priorities, deadlines, dependencies, attachments, and per-job model and thinking effort.
- **Pace them against your real limits.** A token-aggressiveness dial (0–100%) sets how much of your weekly budget to target; the scheduler spreads work across the week rather than draining it on Monday.
- **Reserve time for yourself.** Mark hours where the scheduler stays quiet — which makes it work harder overnight, not give up.
- **See where your tokens actually go.** Timeline by model, breakdowns by skill/subagent/MCP server, token composition, and a waste report showing exactly how much capacity expired unused.
- **Get specific optimization advice**, each finding backed by your own numbers: cache misses, long sessions, model over-selection, effort overuse.
- **Run overnight, safely.** Unattended jobs are isolated in git worktrees with hard per-job spend caps.

## How it works

tokenwarden drives **your own locally installed, already-authenticated `claude` CLI** as a subprocess. It never holds an API key, a token, or a password.

This is deliberate. The Claude Agent SDK expects API-key authentication, which bills per token against the Console and does not draw down your subscription pool — the opposite of what this tool exists to do.

Plan-limit state comes from Claude Code's `statusLine` JSON feed, which is the only documented machine-readable source for `rate_limits.five_hour` and `rate_limits.seven_day`. That feed does not exist in headless mode, so tokenwarden fuses it with an exact local token ledger and a learned calibration. The measurements behind that design are written up in [`docs/SPIKE-001-usage-telemetry.md`](docs/SPIKE-001-usage-telemetry.md).

## Why not just use `/loop` or scheduled tasks?

Claude Code already has `/loop`, Desktop scheduled tasks, and cloud Routines. All of them fire on a clock. **None of them know or care how full your rate-limit window is** — which is the entire problem tokenwarden solves.

## Architecture

A headless Go daemon owns scheduling and serves the UI over `127.0.0.1`; the desktop window is a thin shell over that server.

So it is both a desktop app and a web app: a tray-resident daemon that survives overnight, a native window by default, and the same UI reachable from your phone at 2am to see what ran. Pure-Go SQLite (no cgo) means every platform cross-compiles from one machine.

```
tray + desktop shell (thin)  ──HTTP/SSE──▶  tokenwardend (Go daemon)
                                                │
                                                ├─ exec ─▶ claude -p  (your subscription)
                                                └─ statusline shim ◀─ plan-limit ground truth
```

## Requirements

- A Claude **Pro or Max** subscription. Plan-limit telemetry is not exposed on Free, API-key, or cloud-provider auth.
- Claude Code v2.1.152 or later, installed and logged in.
- macOS, Linux, or Windows.

## Documentation

| Document | Contents |
|---|---|
| [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) | Full requirements, verified research findings, pacing engine design |
| [`docs/SPIKE-001-usage-telemetry.md`](docs/SPIKE-001-usage-telemetry.md) | The experiments that settled the architecture |

## A note on acceptable use

tokenwarden automates your own Claude Code CLI, on your own machine, under your own subscription, for your own work. It does not resell access, proxy claude.ai authentication to anyone else, or expose your rate limits to third parties.

It schedules **real work you have queued**. Manufacturing busywork to burn quota is explicitly not a feature — the point is to stop wasting capacity you already bought, not to consume it for its own sake. You remain responsible for your use complying with [Anthropic's Terms of Service](https://www.anthropic.com/legal/consumer-terms).

## License

MIT — see [LICENSE](LICENSE).
