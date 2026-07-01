# herdr-todo — focus the dropped pane & auto-complete on run drops

**Date:** 2026-06-30 20:09 · **Session ID:** 6f921dda-7308-4da1-b906-479659c18b5b

## Summary

Implemented the top two open follow-ups on the **herdr-todo** plugin, documented
the full install/run flow for both the GitHub and local paths, and cut the first
release tag. Two behavior changes: after a drop the manager now **focuses the
destination pane** (existing-pane drops, matching how new-session drops already
focus their fresh tab), and a **"run" drop auto-marks the todo done** (a "paste"
drop leaves it open). Build / vet / test clean throughout; everything committed,
pushed, and tagged `v0.1.0`.

Repo: `~/projs/go/pers/herdr-todo` (continues the build → textarea-panic-fix →
persistent-pane sessions).

## Starting point

Loaded the three prior sessions as context. The plugin was already: a Go herdr
plugin (`rohanthewiz.herdr-todo`) with an action `todo` + a zoomed `todo-ui`
pane, project + global JSON backlogs, a fuzzy list, and a persistent manager pane
that reuses on re-invoke and survives drops (drops run in-loop, off the UI
thread, reporting back via `dropResultMsg`).

Open follow-ups picked off this session (the first two):

1. Focus the target pane after an **existing-session** drop (previously we only
   `send_input` and stayed in the manager).
2. **Auto-mark a todo done** after a "run" drop.

## Investigation — how to focus a pane by id

Checked the herdr API surface before choosing an approach (herdr Rust source at
`~/projs/rust/herdr`, plus the installed binary's CLI help):

- **No socket method to focus a pane by id.** `src/api/schema.rs` exposes
  `pane.focus_direction` (directional only) and a `pane.focused` *event*, plus
  `tab.focus` / `workspace.focus` — but nothing like `pane.focus <id>`.
- **The CLI has it:** `herdr plugin pane focus <pane_id>` (confirmed in
  `herdr plugin pane --help`). This is the same path `launchTodo` already uses to
  refocus the manager, and `HERDR_BIN_PATH` points at the herdr binary inside a
  plugin command. So the reliable way to focus an existing pane from the TUI is to
  shell out to the CLI, not the socket.
- Note: the `~/projs/rust/herdr` checkout is an **older fork** than the installed
  binary (its `plugin pane` help text isn't even in that source), so the installed
  binary's `--help` output was treated as authoritative.

## Changes made

### 1. Focus the dropped pane (`drop.go`)

- `performDrop`'s `targetExistingPane` case now sends the prompt (real `Enter` in
  run mode, no key in paste mode) and then calls `focusPane(act.target.paneID)`.
- New `focusPane(paneID string) error` — reads `HERDR_BIN_PATH` (falls back to
  `"herdr"`) and runs `herdr plugin pane focus <id>` via `os/exec`. **Best effort:**
  the prompt is already delivered, so a focus failure is ignored and must not fail
  the drop. Added `os` / `os/exec` imports to `drop.go`.
- Net UX: an existing-pane drop now takes you to that pane, mirroring the
  new-session drop (which already focuses its `tab.create(focus=true)` tab).

### 2. Auto-complete on a run drop (`store.go`, `ui.go`)

- `store.setDone(id string, done bool) error` — idempotent (no-op when already in
  that state), so it never reopens an already-done todo. Distinct from `toggle`.
- `dropResultMsg` gained `ref todoRef` + `markDone bool` fields.
- `performDropCmd(ref todoRef, act pendingAction)` now captures the ref and sets
  `markDone := act.mode == dropRun`; `chooseTarget` passes `m.dropTodo` through.
- The `dropResultMsg` handler in `Update`: on **success**, if `markDone`, calls
  `setDone(ref, true)`, appends `· marked done` to the status, and `rebuildList()`.
  On **failure** it just surfaces the error (todo stays open). A **paste** drop
  never sets `markDone`, so it stays open (you haven't committed to it yet).

### 3. Stale-comment fix (`launch.go`)

`runTodoUI`'s doc comment still described the pre-persistence "perform the drop
after the program exits" mechanic (replaced in the persistent-pane session).
Rewrote it to say drops happen in-loop, off the UI thread, and `Run()` only
returns on quit.

### 4. Tests (`model_test.go`)

- `TestRunDropMarksTodoDone` — three subtests: a successful run drop marks done
  **and persists to disk**; a paste drop stays open; a failed run drop stays open
  (surfaces error status). Added a package-level `errTestDrop` sentinel and the
  `errors` / `strings` imports.
- `TestChooseTargetRunMarksDone` — table over run/paste: the emitted
  `dropResultMsg` carries `markDone` true/false respectively and the correct
  `ref`. (Verified they actually run/pass with `go test -run … -v`.)

### 5. Docs (`README.md`)

- "What it does": noted run drops auto-complete (paste leaves open) and that a
  drop focuses the destination pane.
- "How the drop works": existing-pane drop now focuses via
  `herdr plugin pane focus`.
- Rewrote **Install/Use** into a "Getting started" with the full flow for both
  paths (see below), spelling out the Go 1.26+ build-time requirement and the
  action-invoke command.

## The documented run flow

- **From GitHub:** `herdr plugin install rohanthewiz/herdr-todo` (herdr clones,
  runs the `[[build]]` step, registers it). `--ref v0.1.0` pins a tag, `--yes`
  skips the prompt; `herdr plugin uninstall rohanthewiz.herdr-todo` removes it.
- **Local checkout:** `git clone …` → `herdr plugin link .` (or `make link`);
  `make relink` (build + unlink + link) after edits; `make unlink` to remove.
  Checks: `herdr plugin list`, `herdr plugin log list --plugin rohanthewiz.herdr-todo`.
- **Trigger it:** the `Herdr Todo: Prompts` action — from herdr's action menu, a
  bound key, or `herdr plugin action invoke rohanthewiz.herdr-todo.todo` (the
  fully-qualified `<plugin_id>.<action_id>` form, verified in the build session).
  Opens the manager as a zoomed pane scoped to the current project; re-running
  focuses the same pane rather than duplicating it.

Caveat surfaced to the user: the `[[keys.command]]` keybinding route is mentioned
as an option but not hard-coded to a specific key, since it couldn't be verified
against the installed herdr version (older Rust fork checkout).

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` — all pass.
- `gofmt -l *.go` flags only `util.go` (pre-existing, untouched — noted in the
  persistent-pane session).
- **Not** run end-to-end in live herdr (needs herdr driving the pane + a real
  Claude pane; can't be driven headlessly).

## Git / release

- Commit `1f7ee8a` — "Focus dropped pane and auto-complete on run drops"
  (drop.go, store.go, ui.go, launch.go, model_test.go, README.md).
- Commit `5379a05` — "Document the full local and GitHub run flows in the README".
- Both pushed to `origin/main` (was `4d45dc2` at session start).
- Annotated tag **`v0.1.0`** created at `5379a05` and pushed — matches the
  manifest `version = "0.1.0"`, so the README's `--ref v0.1.0` example resolves.
- Session renamed to `herdr-todo-drop-focus-and-autocomplete`.

## Open / unverified

- **Zoom-yield (still unconfirmed on live herdr):** focusing away from the
  *zoomed* manager pane — whether to the new tab or (now) an existing pane —
  assumes herdr yields the screen to the focused pane. If a herdr version keeps
  the zoom on top, the manager should unzoom / yield before/after the drop. Same
  question that was flagged for new-session drops; now also applies to the
  existing-pane focus added here.
- Future releases: bump the manifest `version` alongside the next tag.

## Pre-existing follow-ups still open (from earlier sessions)

- A future TUI panic still silently closes the pane with exit 0 (no on-pane error
  surface). A durable `recover()` around `p.Run()` logging somewhere other than
  the dying pty was discussed but not done.
- Harden the new-session "paste" readiness probe across Claude Code versions.
- Mouse support; reorder todos.

## Develop

```sh
cd ~/projs/go/pers/herdr-todo
make build        # go build -> bin/herdr-todo
make test         # go test ./...
make relink       # rebuild + unlink + link into the running herdr
```
