# Workspace, project skills, attachments, output folder — implementation plan

Written 2026-10-05 so a new agent can pick this up cold. The *what* is in
[`REQUIREMENTS.md`](REQUIREMENTS.md) §5.6 (FR-WS-*, FR-ATT-*, FR-OUT-*,
FR-UI-*), §8 (FR-SAFE-2/3/6/7) and §10 items 5-12; this file is the *how* and
*in what order*. Read those sections first.

## Decisions already made by the user (do not re-ask)

Made 2026-10-05, in conversation, after the user described the goal ("feel
like the native Claude desktop app"):

1. **Every job kind can use the workspace's project skills** and gets the
   **full tool set** (not read-only) — the user explicitly chose to relax the
   old fixed read-only posture for research/plan/review. FR-SAFE-1 (never
   `bypassPermissions`) is untouched. See FR-SAFE-3 for the exact table and
   the one **assumption** to confirm: `plan` keeps `--permission-mode plan`.
2. **Worktree is a per-job choice, default on** (git workspaces); `run_in_place`
   opts out. Non-git workspace → in place + warning.
3. **Output folder is in scope** (`output_dir`, every kind).
4. **Project hooks/MCP: allow, but log and warn** (FR-SAFE-6).
5. Workspace is optional on **all** modes, with a **native folder-picker
   icon** (Finder on macOS); attachments via picker, drag-drop, and clipboard
   paste of screenshots.

## Facts about the current code (verified 2026-10-05)

- `store.Job.Workspace` exists and is set as `cmd.Dir` in
  `internal/runner/runner.go` (PR #32) only when non-empty. A job with none
  inherits the **daemon's cwd** — `/` inside the packaged `.app` — so
  FR-WS-3 (per-job scratch dir) is a real fix, not polish.
- `internal/runner/safety.go` `profileFor`: research/review/plan get
  `readOnlyTools` (`Read, Grep, Glob, WebFetch, WebSearch, NotebookRead` — no
  `Skill`); `code` requires a workspace and always sets `Worktree`; `freeform`
  uses the job's own `PermissionMode/AllowedTools/AddDirs/FreeformWorktree`.
  A fixed kind ignores the job's own tool/permission fields by design — that
  is what FR-SAFE-3 now changes.
- `internal/runner/command.go` `BuildArgs`: prompt is `job.Prompt` (+ the
  resumable progress instruction); attachments only contribute their **parent
  directories** to `--add-dir` (`attachmentDirs`) — the paths are **not**
  mentioned in the prompt. `--worktree` is added when `profile.Worktree ||
  job.Resumable`.
- `store.Attachment{Path, Name}` and the API `attachments` field exist; there
  is no upload endpoint, no CLI flag, no UI. `ui/src/pages/CreateJob.tsx`
  exposes 6 of `CreateJobRequest`'s 18 fields. `ui/src/api/types.ts` mirrors
  the Go DTOs by hand — keep in sync.
- Migrations live in `internal/store/migrations/` (latest `0009`); new job
  fields need a migration plus `store/job.go`, `store/jobs.go`,
  `store/scan.go`, `internal/api/dto.go`, and `queue.PromoteSteps`'
  inheritance list (children must inherit every new safety-relevant field —
  FR-SAFE-3's own lesson, see `docs/SUBAGENT-WORKFLOW.md`).
- The desktop shell (`desktop/main.go`) navigates a window to the daemon's
  URL and deliberately does **not** use Wails' Go↔JS binding. A native folder
  dialog needs a minimal bridge (§10 item 11). The shell's daemon is a child
  process; its config comes from `<data dir>/config.json`
  (`ui_dist_dir`, `claude_binary_path` were set there by hand when the app
  was installed on this Mac).
- Tests must never run the real `claude`. Use `internal/runner/testdata/
  fakeclaude` (add fixtures that echo their argv/cwd so tests can assert
  flags, cwd and prompt content). The daemon logs to
  `<data dir>/tokenwardend.log` (PR #38) — new behaviour should log through
  `slog` in the same key=value style.

## Slices, in order

Each slice is one PR through the normal dev loop. **Classify all of them as
"real correctness surface"** (full pipeline, Opus reviewer): they touch
`internal/runner/safety.go`, job schema, and the safety posture. The
reviewer must explicitly check FR-SAFE-1/2/3 and that new fields are
inherited by `PromoteSteps` children.

**S0 — SPIKE-002 (human-gated; the loop must not run this).** Spends a few
cents of the user's real quota, so get the user's go-ahead first. With the
real `claude` in a scratch git repo that has a `CLAUDE.md`, a project skill
and a `.claude/settings.json` hook, record in `docs/SPIKE-002-workspace-
skills.md`: (a) does `claude -p` loaded from that cwd see `CLAUDE.md` and the
skill? (b) the skill tool's real name and how to allow it in
`--allowedTools` / `dontAsk`; (c) do skills work inside a `--worktree`?
(d) do project hooks/MCP run in `-p` mode, and any trust prompt; (e) is
`--resume` cwd-scoped (§10 item 6); (f) does an unchanged worktree get
cleaned up on exit (§10 item 9); (g) `--add-dir` write access. **S1+ depend
on (b), (c), (e) — do not start them before S0 is recorded** (or if the user
waives it, build against the assumptions and say so in the PR).

**S1 — Runner/safety core.** Workspace optional on every kind
(FR-WS-1/3: per-job scratch dir `<data dir>/jobs/<id>/work`, created by the
runner); kinds → presets with the full tool set + `Skill` (FR-SAFE-3, update
the doc comment table in `safety.go` and the unit tests that pin the old
read-only sets); `run_in_place` field + migration; worktree default for git
workspaces, in-place fallback otherwise (detect git with `git rev-parse`
inside the workspace, not just a `.git` stat); workspace immutable after a
session id exists (FR-WS-6); `workspace_context` + tool-call audit logging
(FR-SAFE-6/7 — parse `tool_use` events in `internal/runner/events.go`).
Never add `bypassPermissions`; keep the budget/lifetime-cap checks intact.

**S2 — Workspace inspection API.** `internal/workspace` (new, depends on
nothing but stdlib): `Describe(path)` → exists/is-dir/readable, git?,
CLAUDE.md?, skill count, hooks?, MCP?. `POST /api/workspace/describe`;
job creation reuses it for validation (FR-WS-4). Cross-platform path
handling (Windows).

**S3 — Attachments.** Staging store + `POST /api/attachments` (multipart),
snapshot into `<data dir>/jobs/<id>/attachments/`, size/count limits
(FR-ATT-4, config keys in `internal/config`), prompt "Attached files" block
in `BuildArgs` (FR-ATT-3), retention sweep (FR-ATT-5, a small goroutine in
the daemon, logged), `--attach` on the CLI (FR-ATT-6), 0700/0600
permissions (FR-ATT-7). Path-form `attachments` in the API keep working but
are copied too. Stop adding the attachment's parent dir to `--add-dir`
(the snapshot replaces that, and the old behaviour exposed a whole folder).

**S4 — Output folder + extra folders.** `output_dir` field + migration;
runner adds it to `--add-dir` and appends the "save files to … do not
overwrite" instruction (FR-OUT-1); before/after listing recorded on the job
(FR-OUT-3, best-effort); `add_dirs` for every kind (FR-WS-8). CLI flags.

**S5 — UI composer.** Rebuild `ui/src/pages/CreateJob.tsx` to the layout in
the user's screenshot (large prompt box, controls row): attachment chips,
drag-drop + clipboard paste (`paste` event → `File` → upload), workspace
field with folder icon, extra folders, output folder, run-in-place toggle,
and the FR-UI-2 "what will happen" summary fed by S2's describe endpoint.
Job detail: attachments + output location (FR-ATT-8, FR-OUT-2). Sync
`types.ts`. Run `task ui:build` and `task ui:lint`; verify in a real browser
against a daemon with the fake claude binary.

**S6 — Native folder picker bridge (desktop shell).** Smallest possible
Wails dialog binding (`desktop/`), exposed to the page as a feature-
detectable function (`window.tokenwarden?.pickFolder`); UI shows the folder
icon in all builds but falls back to focusing the text field when the bridge
is absent. Keep `desktop/` its own module (no cgo in the root). Verify by
hand in the packaged app — CI can only prove it compiles — and note in the
PR that the picker was or wasn't exercised interactively.

**S7 — Docs and install notes.** README (workspace, attachments, output
folder, where files are stored), CLAUDE.md module layout, bump this file's
status. Rebuild/reinstall the app (`task desktop:build`; see the install
notes below).

## Risks to keep in front of you

- **Unattended + `Bash` + a real repo.** The mitigations are FR-SAFE-2
  (worktree), the lifetime budget cap, the kill switch and the audit log.
  Don't "fix" this by quietly restricting tools — the user chose this.
- **Backlog item "workspace isolation gap"** (CLAUDE.md) is now more
  pressing: `cmd.Dir` is a starting point, not a sandbox, and every kind can
  write. Don't resolve it unilaterally; surface it in PRs touching S1.
- **Cost:** `CLAUDE.md` + skills inflate context per run; the predictor is
  per-kind (§10 item 7). Don't change prediction in these slices.
- **Hand-mirrored types:** `ui/src/api/types.ts` ↔ `internal/api/dto.go`.
- **Install on this Mac** (so a rebuilt app actually reflects the change):
  `/Applications/Tokenwarden.app` bundles `tokenwardend` and `ui-dist`;
  rebuilding means `task ui:build`, `task desktop:build`, strip xattrs
  (`xattr -cr`, OneDrive adds Finder info that breaks `codesign`), copy the
  fresh `bin/tokenwardend` and `ui/dist` into the bundle, re-sign ad hoc.
  Quit the running app from the tray first.
