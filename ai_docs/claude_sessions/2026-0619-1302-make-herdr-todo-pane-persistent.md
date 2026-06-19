# herdr-todo — make the manager pane persistent

**Date:** 2026-06-19 13:02 CDT · **Session ID:** 019379ce-0c52-4293-ae0d-04ba83ebeefb

## Summary

Made the **herdr-todo** manager pane persistent in two ways the user explicitly
chose (via a clarifying question): **reuse an already-open pane** instead of
spawning a duplicate, and **survive drops** so the manager stays open after
dropping a prompt. The third option offered — "survive quit (esc)" — was
**declined**, so `esc`/`ctrl+c` remains the deliberate way to close the pane.

Before this change every `todo` action opened a fresh zoomed plugin pane, and the
pane closed on *every* drop (the drop was deferred until after the TUI exited).
Now the pane is a long-lived, reusable instance.

Build / vet / test all clean; changes **uncommitted** on `main`.

Repo: `~/projs/go/pers/herdr-todo` (continues the build + textarea-panic-fix
sessions).

## Architecture recap (how the plugin is wired)

- herdr plugin: `herdr-plugin.toml` registers an **action** `todo` and a **pane**
  entrypoint `todo-ui` (title `"Herdr Todo"`, `placement = "zoomed"`).
- `main.go` dispatches subcommands: `todo` (the action, runs **server-side** with
  no tty) and `todo-ui` (the Bubble Tea TUI, run by herdr **inside the pane**).
- `launchTodo()` (server-side) execs `herdr plugin pane open … --entrypoint
  todo-ui` and passes the launch context as base64 JSON in `HERDR_TODO_CTX`.
- `runTodoUI()` decodes that context, loads the project + global stores, and runs
  the TUI.
- **Key herdr fact (from the prior session):** herdr closes a plugin pane when its
  command **process exits**. So "persistent pane" == "keep the `todo-ui` process
  alive / don't exit on drop" + "focus the existing one on re-invoke."
- herdr socket client (`herdr.go`) speaks newline-delimited JSON over
  `HERDR_SOCKET_PATH` (injected into every plugin command, including the action).
- CLI surface available: `herdr plugin pane open|focus <id>|close <id>` (no pane
  *list* in the CLI — `pane.list` is socket-only).

## Changes made

### 1. Survive drops — drop in-loop, off the UI thread (`ui.go`, `launch.go`)

Previously `chooseTarget` set `m.action` + `quitting = true` + `tea.Quit`, and
`runTodoUI` performed the drop **after** `p.Run()` returned (so the pane was gone
by then). Replaced with an asynchronous in-loop drop:

- New `dropResultMsg{desc string, err error}` type.
- `model.action *pendingAction` field **removed**; new `model.dropping bool`
  field (in-flight guard).
- `chooseTarget` now: guards on `m.dropping`, builds the `pendingAction`, sets
  `m.dropping = true`, returns to `stageList`, sets a `"dropping into …"` status,
  and returns `m.performDropCmd(act)` — it no longer quits.
- New `performDropCmd(act) tea.Cmd` runs `performDrop(client, ctx, act)` in a
  goroutine and returns a `dropResultMsg`. Client + ctx captured by value;
  `performDrop` only makes short-lived independent socket calls, so it's safe
  alongside the still-rendering UI. Async matters because a new-session drop takes
  seconds (tab create + wait for claude to draw) and would otherwise freeze the
  TUI.
- `Update` gained a `case dropResultMsg`: clears `m.dropping`, shows
  `"dropped → <desc>"` or `"drop failed: …"`.
- `beginDrop` gained a `m.dropping` guard ("a drop is still in progress…").
- New helper `targetDesc(dropTarget)` → `"new Claude Code session"` /
  agent name / `"session"` fallback, used in the status line.
- `runTodoUI` lost the post-exit drop block; `Run()` now only returns on quit.

Resulting UX: a **new-session** drop focuses the fresh Claude tab and leaves the
manager alive in the background; an **existing-pane** drop keeps you in the
manager so you can drop again.

### 2. Reuse the existing pane (`launch.go`)

- New const `paneTitle = "Herdr Todo"` (matches the manifest pane title; herdr
  reports it as both `Title` and `Label` on `pane.list` — confirmed in the prior
  session's notes).
- New `existingTodoPane(ctx RunContext) (string, bool)`: opens the socket client,
  lists panes, returns the first whose `Title`/`Label` == `paneTitle`, scoped to
  `ctx.WorkspaceId` when both that and the pane's `WorkspaceID` are non-empty (so
  each project keeps its own correctly-scoped manager). Returns `("", false)` if
  the socket is unavailable or nothing matches → caller opens fresh.
- `launchTodo` now calls `existingTodoPane` first and, on a hit, runs `herdr
  plugin pane focus <id>`; only on focus failure / no hit does it fall through to
  the original `pane open` path. Moved `ctx.encode()` below the reuse check (only
  needed when actually opening).

### 3. Docs (`README.md`)

- Added a **"A persistent pane."** bullet under "What it does".
- Added a note under "How the drop works" that the drop runs off the UI thread
  while the pane stays alive.
- Updated the Layout line for `launch.go` ("focuses or opens the UI pane").

### 4. Tests (`model_test.go`)

- `TestChooseTargetStaysOpen` — choosing a target doesn't quit; sets `dropping`,
  returns to list with a non-error status, returns a command; executing the
  command yields a `dropResultMsg` (errors with nil socket) and `Update` clears
  the flag + surfaces the error.
- `TestBeginDropWhileDroppingIsRejected` — second drop blocked while one is in
  flight.
- `TestTargetDesc` — the three label cases.

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` — all pass.
- `gofmt -l *.go` flags only `util.go`, which is **pre-existing and untouched**
  by this change (left alone, out of scope).
- The `paneSplit` / `tabRename` "unused method" hints in `herdr.go` are
  pre-existing, not introduced here.

## Decisions & rationale

- **Asked before building.** "Make the pane persistent" had ≥3 plausible meanings
  with different risk. The user picked *reuse* + *survive drops*, not *survive
  quit*. Kept `esc`/`ctrl+c` as the close path.
- **Workspace-scoped reuse.** New sessions are already rooted by workspace, and
  project todos scope to where the pane opened, so reusing only within the same
  workspace keeps the displayed scope correct. Falls back to title-only match if
  herdr reports an empty `WorkspaceID` for the pane — better to reuse than
  duplicate.
- **CLI for focus, socket for list.** `herdr plugin pane focus <id>` is guaranteed
  to exist; `pane.list` has no CLI equivalent, so the socket is used for discovery
  only. Avoids guessing whether a `pane.focus` socket method exists.

## Open / unverified

- **Could not run end-to-end** (needs live herdr driving the pane + a real Claude
  pane; can't be driven headlessly). Verified via unit tests + build only.
- **Zoom-yield question for the user:** for a new-session drop,
  `tab.create(focus=true)` must yield the screen from the *zoomed* manager pane to
  the new Claude tab. If a given herdr version keeps the zoom on top, the manager
  should unzoom / yield focus before the drop — flagged to the user to confirm on
  real herdr.
- To try it: `make relink`, then invoke the action twice (second should focus the
  same pane) and drop a prompt (manager should stay open).

## Pre-existing follow-ups still open (from earlier sessions)

- Focus the target pane after an **existing-session** drop (currently you stay in
  the manager; we only `send_input`).
- Mark a todo done automatically after dropping it.
- A future TUI panic still silently closes the pane with exit 0 (no on-pane error
  surface).
