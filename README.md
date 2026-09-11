# tokenwarden

## Note to readers

This repo is intentionally AI driven. For me, solving the problem below does not warrant enough effort from me to develop this application with lots of human guidance.
To me, this is a great use case of "vibe" coding a highly specific, customized software solution to meet my specific needs. There are software projects that I do
want to put lots of human effort into to learn, understand, and maintain a good project. but this is not one of them. This project simply aims to help me use the tokens I've already paid for
to help research things for me or work on other quality of life projects that would be nice to have but don't warrant enough effort to develop myself.

## Overview

**Stop wasting the Claude subscription capacity you already paid for.**

A Claude Pro/Max subscription is metered by two rolling windows — a 5-hour session window and a 7-day weekly window. Unused capacity in either **does not roll over**. It refills while you sleep, and expires unused not because there is no work to do, but because nobody was at the keyboard to dispatch it.

tokenwarden is a cross-platform desktop app that holds a queue of real work and paces it against your own Claude subscription, so weekly capacity lands as close to fully used as you choose — while reserving headroom for the hours you want Claude for yourself.

> **Status: early alpha.** The daemon, CLI, a basic local web UI, and a native desktop shell (macOS) all work end to end — you can queue jobs, dispatch them against your real `claude` CLI, and watch usage/scheduler state in a browser or in a real window with a system tray. The budget-aware dispatch loop runs but stays a no-op until you opt in. Not yet built: Windows/Linux desktop packaging, the active (PTY sentinel) capture path, and the usage timeline/breakdown views. See [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) and [`CLAUDE.md`](CLAUDE.md#current-phase) for exactly what's done.

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
- Claude Code v2.1.152 or later, installed and logged in (the `claude` binary must be on your `PATH`).
- macOS, Linux, or Windows.
- [Go](https://go.dev/dl/) 1.25+ to build the daemon and CLI.
- [Node.js](https://nodejs.org/) 20+ (only if you want the web UI — the daemon and CLI build and run fine without it). If your system Node is older, [nvm](https://github.com/nvm-sh/nvm) is the easiest way to get a current one: `nvm install --lts && nvm use --lts`.
- [Task](https://taskfile.dev) (optional but recommended — wraps the commands below): `brew install go-task`, or `go install github.com/go-task/task/v3/cmd/task@latest`.

## Building and running

There's no packaged release yet — build from source. All commands below assume the repo root as your working directory.

### 1. Build the daemon and CLI

```bash
task build          # → ./bin/tokenwardend, ./bin/tokenwarden, ./bin/twprobe
```

Without `task`, the equivalent is:

```bash
go build -o bin/ ./cmd/tokenwardend
go build -o bin/ ./cmd/tokenwarden
go build -o bin/ ./cmd/twprobe
```

### 2. Run the daemon

```bash
task run             # builds + runs tokenwardend in the foreground
# or directly:
./bin/tokenwardend
```

By default it listens on `127.0.0.1:7842` and stores its database in the OS-appropriate per-user config directory (e.g. `~/Library/Application Support/tokenwarden` on macOS). Override with `TOKENWARDEN_LISTEN_ADDR`, `TOKENWARDEN_DATA_DIR`, or `TOKENWARDEN_DB_PATH` env vars, or a `config.json` in that same directory — see `internal/config` for the full list.

### 3. Drive it from the CLI

With the daemon running (in another terminal):

```bash
./bin/tokenwarden status
./bin/tokenwarden queue add --kind research --prompt "Summarize the open issues in this repo"
./bin/tokenwarden queue list
./bin/tokenwarden queue dispatch <job-id>       # runs it against your real claude CLI
./bin/tokenwarden usage                          # 5h/7d window usage, ground truth, calibration
./bin/tokenwarden scheduler config set --enabled --aggressiveness 60
./bin/tokenwarden kill-switch status
```

Run `./bin/tokenwarden` with no arguments for the full command list. Set `TOKENWARDEN_ADDR` if the daemon isn't on its default address.

**Optional:** `./bin/tokenwarden probe install` wires `twprobe` into Claude Code's `statusLine` setting, so your own interactive sessions feed the daemon real rate-limit readings for free (see [`docs/SPIKE-001-usage-telemetry.md`](docs/SPIKE-001-usage-telemetry.md) for why this is the only way to get that signal today).

### 4. Run the web UI

The UI is a separate local web app the daemon serves — see `ui/` and [`CLAUDE.md`](CLAUDE.md) for the full design.

**For local development** (hot reload, points at an already-running daemon):

```bash
task ui:install      # once
task ui:dev           # Vite dev server at http://localhost:5173
```

**For a production build**, served by the daemon itself at its own address (`http://127.0.0.1:7842` by default) — no separate dev server needed:

```bash
task ui:build          # outputs ui/dist
task run                # (re)start the daemon — it picks up ui/dist automatically if present
```

If `ui/dist` doesn't exist, the daemon just runs API-only — building the UI is entirely optional.

### 5. Run the native desktop app (macOS)

A thin native window and system tray around the daemon — see `desktop/` and [`CLAUDE.md`](CLAUDE.md) for the design. It's a separate Go module (Wails v3 needs cgo, which the daemon/CLI/probe deliberately don't) with its own toolchain requirement: [Wails' platform prerequisites](https://v3.wails.io/getting-started/installation/) (Xcode command line tools on macOS) plus the `wails3` CLI:

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.16
```

```bash
task ui:build          # the shell points its window at the daemon's UI, so build it first
task desktop:build     # produces desktop/bin/desktop.app
open desktop/bin/desktop.app
```

Closing the window hides it rather than quitting (the scheduler needs to keep dispatching unattended) — use the tray icon's Quit to actually stop it, which also stops the daemon it spawned. `task desktop:dev` (`go run .` in `desktop/`) is faster for iterating on the shell itself. Windows/Linux builds aren't wired up yet — see [`CLAUDE.md`](CLAUDE.md) for what's left.

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
