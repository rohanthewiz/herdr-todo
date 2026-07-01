# herdr-todo — store hardening & a seven-item UX batch

**Date:** 2026-07-01 15:56 · **Session ID:** ecb4f1c0-9e1d-479d-a3b4-97f668598e1b

## Summary

A full improvement pass on the **herdr-todo** plugin. The session began as a
"what improvements do you recommend?" review of the whole codebase, detoured
through a storage-engine evaluation (DuckDB considered and rejected), and landed
on: **harden the existing JSON-array store** (atomic saves, reload-before-mutate,
honest not-found errors) plus **UX items 4–10** from the review — full-body
fuzzy filtering, a read-only prompt view, done-item management, reordering, a
quick-capture CLI, multi-agent new sessions, and a unified run/paste drop path.
Build / vet / gofmt / tests clean; the new `add` CLI was exercised end-to-end in
a sandbox. Nothing committed yet.

Repo: `~/projs/go/pers/herdr-todo` (continues the build → textarea-panic-fix →
persistent-pane → drop-focus sessions).

## Phase 1 — codebase review & recommendations

Read the entire plugin (~2,200 lines + tests) and delivered a ranked list:

**Data safety (1–3):** atomic saves (`os.WriteFile` could truncate on crash);
reload-before-mutate (every pane shares the global `todos.json`, so a stale
in-memory copy silently clobbered other panes' writes); `update`/`toggle`/
`setDone` returned `nil` for unknown IDs (stale panes reported false success).

**UX gaps (4–7):** the fuzzy filter only matched the first-line preview; no way
to read a prompt without opening the edit form; done items piled up inline
forever; no reordering.

**Features (8–10):** no shell-side quick capture; new-session drops hardcoded to
`claude` while existing-pane targets already listed any agent; run-mode new
sessions used `claude '<prompt>'` (shell-quoting risk, prompt visible in shell
history and `ps`).

**Cleanups:** redundant `"? for shortcuts"` ready-probe; `drop.go`/`herdr.go`
untested; `ui.go` size.

## Phase 2 — the storage decision

- User proposed **DuckDB** (Go client). Flagged the constraints: CGO-based
  driver (`marcboeker/go-duckdb/v2`, big binary, C toolchain at plugin-install
  time), single read-write process per DB file (breaks the multi-pane global
  backlog unless opened per-operation), and the committed project backlog
  becoming an opaque binary in git.
- User then asked about **JSONL** and lighter options. Assessment: JSONL is
  worse for this data (multi-line prompts collapse to one escaped line; edits
  rewrite the file anyway); one-markdown-file-per-todo is the most git-native
  sleeper option; embedded DBs inherit the binary-in-git problem.
- **Decision: keep the pretty-printed JSON array and harden it.**

## Phase 3 — implementation

### Store hardening (`store.go`)

- `save()` now writes to a temp file in the same directory and `os.Rename`s over
  the target — the file is always the old or the new contents, never truncated.
- New `reload()` (skips unavailable stores, which `load()` would wipe); every
  mutation (`add`/`update`/`delete`/`toggle`/`setDone`) reloads from disk before
  applying itself, so concurrent manager panes never lose each other's writes.
- New `errTodoNotFound` ("changed from another pane?") returned by mutations on
  missing IDs.
- New `move(id, delta)` — swaps only with neighbors in the same done state
  (matches the view: open first, done last); array order **is** the priority
  order. New `clearDone()` — removes all done todos, returns the count, skips
  the save when zero.

### UX items (ui.go, fuzzylist.go, drop.go, new cli.go)

4. **Full-body filtering** — `listItem` gained a `search` field (title + prompt
   flattened to one line) that replaces `desc` in the fuzzy haystack; name-only
   highlighting preserved.
5. **`ctrl+v` view stage** — read-only, scrollable (`bubbles/viewport`) view of
   the full prompt with a meta line (scope · added date · done). From it:
   `enter` drops, `ctrl+e` edits, `esc` back. `beginDrop`/`beginEdit` were
   refactored into ref-taking `startDrop`/`beginEditRef` so both list and view
   stages share them.
6. **Done management** — done items sort below open ones within each scope
   group; `ctrl+d` folds them away (header shows "N done hidden"); `ctrl+w`
   bulk-deletes them behind the confirm stage (which gained a `confirmKind`
   enum: delete-one vs clear-completed).
7. **Reordering** — `ctrl+↑`/`ctrl+↓` call `store.move`; the highlight follows
   the moved todo via new `fuzzyList.selectRef` + `model.selectRow`.
8. **`herdr-todo add` CLI** (`cli.go`) — `add [-g|-global] [-t|-title] [prompt…]`;
   prompt from args or piped stdin (never blocks on a TTY); project root =
   nearest `.herdr-todo` dir above cwd, else nearest `.git`, else cwd
   (`findProjectRoot`). Dispatched from `main.go`.
9. **Multi-agent new sessions** — `dropTarget` gained `command`; `buildTargets`
   offers "＋ New _agent_ session" for each distinct non-claude agent currently
   running in a pane (its detected agent label is used as the launch command),
   claude always first. `targetDesc` reflects non-claude sessions.
10. **Unified run mode** — new-session drops always launch the agent bare, wait
    for readiness (`claudeReadyProbes` for claude with 12 s timeout; a fixed
    2.5 s grace period for other agents via `waitForAgentReady`), then
    `sendInput(prompt)` — with a real `Enter` key appended in run mode.
    `shellQuote` deleted (with its tests); no prompt in shell history / `ps`.

### Docs & tests

- README: feature bullets, two-row key table addition (View stage row), new
  quick-capture section, rewritten "How the drop works", updated Layout.
- Footer in the list view is now two lines to fit the new keys.
- Tests updated/added: store reload/atomicity/move/clearDone/not-found; model
  tests for done-to-bottom sorting, hide-done fold, deep-line filtering,
  reorder-with-highlight, clear-completed confirm flow, and the view stage;
  shellQuote tests removed.

## Verification

- `gofmt -l` clean, `go vet ./...` clean, `go test ./...` **ok**.
- Sandbox run of the CLI: add from a repo subdirectory landed at the repo root's
  `.herdr-todo/todos.json`; piped-stdin add worked; `-g` with
  `HERDR_PLUGIN_CONFIG_DIR` targeted the global file; empty prompt exited 2 with
  usage.

## Key bindings after this session

| Stage  | Keys |
| ------ | ---- |
| List   | `enter` drop · `ctrl+v` view · `ctrl+a` add · `ctrl+e` edit · `ctrl+t` toggle done · `ctrl+x` delete · `ctrl+↑/↓` move · `ctrl+d` hide/show done · `ctrl+w` clear done · type to filter · `esc` clear/quit |
| View   | `↑/↓` scroll · `enter` drop · `ctrl+e` edit · `esc` back |
| Form   | `tab` field · `ctrl+s` save · `ctrl+g` scope (add) · `esc` cancel |
| Target | `enter` paste · `ctrl+r` drop & run · `esc` back |

(`ctrl+h` was avoided deliberately — terminals send it as backspace.)

## Open follow-ups

- Non-claude agent readiness is a fixed 2.5 s grace period — could learn
  per-agent probes later.
- `drop.go`/`herdr.go` still lack tests; a fake unix-socket server would cover
  `runCommand` pacing and `performDrop` branching.
- Claude ready-probes track Claude Code UI strings and may need refreshing.
- `ui.go` is now ~1,000 lines; split per stage if it grows again.
- Not yet committed — needs a commit and (optionally) a `make relink` to try
  live in herdr.
