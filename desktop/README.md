# tokenwarden desktop shell

A thin native (Wails v3) window/tray around the `tokenwardend` daemon — see
[`main.go`](main.go)'s package doc for what it does and why it's a separate
Go module, and the repo root [`CLAUDE.md`](../CLAUDE.md) for the project
overview.

This module has no product logic of its own: it spawns `tokenwardend`,
waits for `/api/health`, and points a window at the daemon's own address.
All actual functionality (the API, the web UI) lives in the root module.

## Commands

Run from the repo root:

```bash
task desktop:dev     # go run . against a freshly built daemon, for iteration
task desktop:build   # a real .app bundle via `wails3 build`/`package`
```

`wails3` (the CLI, not just the `v3` library this module depends on) needs
to be installed separately: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.16`.

## Icon

`assets/icon-master.svg` and `assets/tray-template.svg` are hand-composed
from `ui/public/favicon.svg` (the app's existing brand mark) — see those
files' comments for how they were centered/simplified. `build/appicon.png`
is the 1024×1024 raster Wails' own tooling (`wails3 task
common:update:build-assets`) generates every other platform icon format
from; re-run that task after changing the source SVGs.
