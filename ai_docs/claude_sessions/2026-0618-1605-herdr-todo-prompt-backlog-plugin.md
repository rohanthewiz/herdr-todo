# herdr-todo — a prompt-backlog herdr plugin (initial build)

**Date:** 2026-06-18 16:05 · **Session ID:** 735fb5b1-43da-418b-a4e1-64bb838a409e

## Summary

Built **herdr-todo** from scratch: a new, standalone [herdr](https://herdr.dev)
plugin that acts as a *prompt backlog*. You jot down prompts of future work
(per-project and global), then fuzzy-pick one and **drop it into a Claude Code
session** — either an already-running agent pane herdr has detected, or a
brand-new session it spins up in the current project. The plugin is written in
Go, modeled on `cloudmanic/herdr-plus`, builds clean, and is linked and live in
the running herdr.

Repo: `~/projs/go/pers/herdr-todo` (new, `git init`, not yet committed).

## Context / starting point

- The task began in `~/projs/go/pers/kro` (a k8s monitor) but was redirected to
  herdr. herdr's runtime config lives at `~/.config/herdr` (`config.toml`,
  sockets `herdr.sock` / `herdr-client.sock`, `plugins/`, `plugins.json`,
  `session.json`). The herdr binary itself is on PATH (`/opt/homebrew/bin/herdr`)
  and its Rust source is at `~/projs/rust/herdr`.
- The only installed plugin was `cloudmanic.herdr-plus` (a managed GitHub
  install under `~/.config/herdr/plugins/github/...`). It is **MIT licensed**
  despite "all rights reserved" file headers, so its infra was safe to adapt
  with attribution.

### How herdr plugins work (learned from herdr-plus + herdr source)

- A plugin is a single binary herdr drives via subcommands; `herdr-plugin.toml`
  declares `[[actions]]`, `[[panes]]`, `[[events]]`.
- An **action** runs server-side (no TTY); it typically calls
  `herdr plugin pane open --plugin … --entrypoint … --placement … --env …` to
  open a **pane** that runs a Bubble Tea TUI.
- The TUI talks to herdr over a unix socket (`HERDR_SOCKET_PATH`) using
  newline-delimited JSON-RPC. Verified method/shape against the Rust source
  (`src/api/schema/panes.rs`, `src/app/api/panes.rs`, `src/detect/mod.rs`):
  - `pane.list` / `pane.get` return `PaneInfo` including a per-pane **`agent`**
    label (lowercase, e.g. `"claude"`, `"codex"`) — so running Claude sessions
    are discoverable directly from `pane.list`.
  - `pane.send_input` types text and optional keys; a real `"Enter"` key submits
    (an embedded `\n` in `text` is pasted literally, not submitted).
  - `tab.create`, `workspace.create/get`, `pane.split` build new sessions.
- Config: herdr injects `HERDR_PLUGIN_CONFIG_DIR` (per-plugin, survives
  upgrades); launch context arrives via `HERDR_PLUGIN_CONTEXT_JSON` and is
  forwarded to the pane as a base64 env var.
- `claude [prompt]` launches an interactive session seeded with that prompt
  (used for the new-session "run" path).

## Design decisions (confirmed with the user up front)

1. **Location:** brand-new standalone plugin (owned by the user), not a fork or
   an edit of the managed herdr-plus install.
2. **Scope:** both project **and** global backlogs, grouped in the list.
3. **Drop target:** both an existing detected Claude session **and** a new one.
4. **Submit behavior:** per-drop choice — `enter` pastes without submitting,
   `ctrl+r` drops & runs.

## What was built

New module `github.com/rohanthewiz/herdr-todo` (`go 1.26`), plugin id
`rohanthewiz.herdr-todo`.

### Files

```
herdr-plugin.toml   manifest: `todo` action + `todo-ui` zoomed pane
main.go             subcommand dispatch (todo / todo-ui / version)
launch.go           `todo` action (opens UI pane) + `todo-ui` runner; performs the drop after the TUI exits
ui.go               Bubble Tea manager: list / form / confirm / target stages
store.go            project + global JSON-backed todo storage
drop.go             send prompt to an existing pane or a new claude session
herdr.go            herdr unix-socket JSON-RPC client (adapted from herdr-plus)
context.go          launch-context plumbing (adapted from herdr-plus)
fuzzylist.go        reusable fuzzy list TUI component (adapted from herdr-plus)
styles.go           lipgloss palette
util.go             errExit, firstNonEmpty, shellQuote, newID, firstLine, truncate
scripts/build.sh    go build -> bin/herdr-todo (the [[build]] step)
Makefile            build / vet / test / link / unlink / relink
README.md           usage + design docs
LICENSE (MIT) + NOTICE (Cloudmanic attribution) + .gitignore
```

### Storage

- **Project** todos: `<repo>/.herdr-todo/todos.json` (travels with the repo).
- **Global** todos: `<HERDR_PLUGIN_CONFIG_DIR>/todos.json` (falls back to
  `$XDG_CONFIG_HOME/herdr-todo` or `~/.config/herdr-todo` outside herdr).
- `Todo{ id, title, prompt, done, created }`; the list groups `Project`/`Global`
  only when both have entries (mirrors herdr-plus's project/global grouping).

### TUI stages & keys

- **List:** `enter` drop · `ctrl+a` add · `ctrl+e` edit · `ctrl+t` toggle done ·
  `ctrl+x` delete · type to fuzzy-filter · `esc` clears filter then quits. Done
  todos render struck-through with a `✓`.
- **Form:** title (`textinput`) + multi-line prompt (`textarea`); `tab` switches
  fields, `ctrl+s` saves, `ctrl+g` toggles Project/Global (add mode), `esc`
  cancels. Empty title is derived from the first line of the prompt.
- **Confirm delete:** `y`/`enter` delete · `n`/`esc` cancel.
- **Target:** "＋ New Claude Code session" plus every detected agent pane
  (Claude-first, own pane excluded, `(this project)` annotated); `enter` pastes,
  `ctrl+r` drops & runs.

### The "drop" mechanic (`drop.go`, run after the TUI exits)

- **Existing pane:** `pane.send_input(paneID, prompt)` — adding `"Enter"` in run
  mode.
- **New session:** `tab.create` in the project workspace, then:
  - *run* → `claude <shell-quoted prompt>` (Claude starts on it immediately);
  - *paste* → bare `claude`, wait (best-effort poll for its input UI via
    `claudeReadyProbes`), then `send_input` without Enter.
- Deferring the drop until after `p.Run()` exits matches herdr-plus's pattern
  (create panes / switch workspaces once our own zoomed pane tears down).

## Verification

- `go build` ✅ · `go vet` ✅ (gopls "not in workspace" warnings are an IDE
  artifact only — the module isn't in the editor's `go.work`).
- `herdr-todo version` / bare-usage ✅.
- `herdr plugin link .` ✅ → registered, enabled; action `Herdr Todo: Prompts`
  and pane `todo-ui` recognized; config dir provisioned
  (`~/.config/herdr/plugins/config/rohanthewiz.herdr-todo`); empty plugin log.
- `todo-ui` init path (decode ctx → load stores → build model) runs headless
  without panic (only stops at the TTY, as expected).
- `herdr plugin action invoke rohanthewiz.herdr-todo.todo` ✅ → action log
  `succeeded`, empty stderr; UI opened as a zoomed pane in the **Tools**
  workspace. herdr's captured context showed the focused pane was a `claude`
  agent in `~/projs/go/pers/kro`; two running Claude sessions detected, so both
  appear as drop targets.

## Notable details / gotchas

- A bare-letter command scheme would clash with type-to-filter, so all list
  commands use `ctrl`-chords; navigation stays on arrows + `ctrl+p`/`ctrl+n`.
- `rebuildList` was simplified to create the `fuzzyList` once in `newModel` and
  only `setItems` on refresh, so add/edit/toggle never resets the filter/cursor.
- Pin deps to herdr-plus's versions (bubbletea 1.3.10, bubbles 1.0.0, lipgloss
  1.1.0, sahilm/fuzzy 0.1.3) so they resolve from the existing module cache.

## Follow-ups / ideas

- Initial git commit (repo is `git init`'d but uncommitted).
- Optional: focus the target pane after an existing-session drop; mark a todo
  done automatically after a "run" drop; mouse support; reorder todos.
- Harden the new-session "paste" readiness probe across Claude Code versions.

## Develop

```sh
cd ~/projs/go/pers/herdr-todo
make build        # go build -> bin/herdr-todo
make relink       # rebuild + unlink + link into the running herdr
```
