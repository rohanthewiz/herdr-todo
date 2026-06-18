# herdr-todo — fix the pane that wouldn't show (textarea startup panic)

**Date:** 2026-06-18 16:45 · **Session ID:** 735fb5b1-43da-418b-a4e1-64bb838a409e

## Summary

Diagnosed and fixed the bug where invoking the **herdr-todo** plugin "succeeded"
but no todo pane appeared. The pane *was* opening — but the `todo-ui` process
**panicked ~30 ms after launch**, so herdr tore the pane down before anything
rendered. Root cause: `applySizes` resized the form's `textarea` on the first
`WindowSizeMsg`, while that textarea was still a zero-value model. Fixed by making
`applySizes` stage-aware, then added a regression test that reproduces the exact
crash. Build / vet / test all clean; changes uncommitted.

Repo: `~/projs/go/pers/herdr-todo` (continues the initial-build session).

## Symptom

User ran `herdr plugin action invoke rohanthewiz.herdr-todo.todo` from the CLI.
It returned `status: running` / `succeeded`, but the zoomed Todo pane never
showed. The visible pane stayed on the user's shell (`~/xfr`, the `claude` tab in
the **Tools** / `wD` workspace).

## Investigation (the load-bearing part)

A chain of evidence, each step narrowing the cause:

1. **Plugin action log** (`herdr plugin log list --plugin rohanthewiz.herdr-todo`)
   showed the action **succeeded, exit 0**, and its stdout was a
   `plugin_pane_opened` event — pane `wD:p6`, `focused:true`. So `herdr plugin
   pane open` worked; the pane *was* created.

2. **Live pane list** over the herdr socket
   (`printf '{"id":"x","method":"pane.list","params":{}}' | nc -U ~/.config/herdr/herdr.sock`)
   showed **no `wD:p6`** — it had been created and torn down. herdr closes a
   plugin pane when its command process exits. So `todo-ui` was exiting.

3. **PTY repro** — running `./bin/herdr-todo todo-ui` under `script` (a real PTY)
   **stayed alive**. Running it with `stdin=/dev/null` exited 0 with
   `could not open a new TTY: open /dev/tty: device not configured`. This *looked*
   like the cause (Bubble Tea wanting a TTY for input), but…

4. **…herdr-plus fails identically** with `stdin=/dev/null` (same Bubble Tea
   v1.3.10, same `WithAltScreen`) yet works fine in real herdr — so the
   `/dev/null` repro was **not representative**. herdr *does* give plugin panes a
   real TTY. Theory discarded.

5. **herdr server log** (`~/.config/herdr/herdr-server.log`) was decisive:
   ```
   pane child spawned   pane_id=7 pid=19236
   pane child exited    pane_id=7 status=ExitStatus { code: 0 }   (~30 ms later)
   pane session terminated pane=7 signal=Hangup
   ```
   Clean exit 0 in 30 ms — not a TTY error (those took 350–636 ms), something
   faster and earlier.

6. **In-pane instrumentation** (temporary) — added a `dbg()` helper writing to
   `/tmp/herdr-todo-debug.log` (stderr dies with the pane, so a file was needed),
   logging TTY state, env, and the `p.Run()` error. Relinked, re-invoked. Result:
   - `stdin/stdout/stderr isatty=true`, `TERM=xterm-256color`, socket present,
     `loadStores ok` — **all healthy**.
   - `tea program returned err=program was killed: program experienced a panic`.
   So: a **panic**, hidden because Bubble Tea catches it, restores the terminal,
   and returns a generic error that `runTodoUI` prints to the dying pty (exit 0).

7. **Captured the stack** — added `tea.WithoutCatchPanics()` + a `recover()` that
   logs `debug.Stack()`. Relinked, re-invoked:
   ```
   panic: nil pointer dereference
     github.com/charmbracelet/bubbles/textarea.(*Model).SetWidth(..., 0x49)
     main.(*model).applySizes              ui.go:705
     main.model.Update (WindowSizeMsg)     ui.go:133
   ```

## Root cause

`applySizes` ran on the first `WindowSizeMsg` (which a real herdr pane sends on
open) and unconditionally called `m.promptArea.SetWidth()` / `SetHeight()`. But
`promptArea` (textarea), `titleInput`, and `targetList` are **zero-value until
their stage is entered** — they're built lazily in `beginAdd` / `beginEdit` /
`beginDrop`. Calling a *method* on a zero-value `textarea.Model` dereferences nil
internal state → panic. (The direct `.Width =` field assignments on the other
zero-value inputs are harmless; only the textarea method calls crash.)

Why it slipped past the initial-build smoke test: that headless path never
delivers a real `WindowSizeMsg` (size 0 → the `w < 20` guard returns early), so
`applySizes` never reached the textarea call.

## The fix (`ui.go`)

Made `applySizes` **stage-aware** — only resize the components belonging to the
live stage, so it never touches an uninitialized one:

```go
func (m *model) applySizes() {
	w := m.width - 4
	if w < 20 {
		return
	}
	m.list.input.Width = w
	switch m.stage {
	case stageForm:
		m.titleInput.Width = w
		m.promptArea.SetWidth(w)
		if h := m.height - 12; h >= 4 {
			m.promptArea.SetHeight(h)
		}
	case stageTarget:
		m.targetList.input.Width = w
	}
}
```

All diagnostic instrumentation (the `dbg` helper, `recover`, `WithoutCatchPanics`,
and the `x/term` / `runtime/debug` / `time` imports) was removed afterward —
`launch.go` is back to clean production code.

## Verification (end to end in real herdr)

- Rebuilt, relinked, re-invoked the action. The live pane list now shows
  `wD:p9  label="Herdr Todo"` **persisting**, and `pane.read` confirms the TUI
  renders:
  ```
   📝  Herdr Todo — prompt backlog   Tools + global
  ❯ Type to filter prompts…
    No prompts yet — press ctrl+a to add one.
  enter drop · ctrl+a add · ctrl+e edit · ctrl+t done · ctrl+x delete · esc quit
  ```
- `go build` ✅ · `go vet` ✅ · `go test ./...` ✅.

## Regression test (`ui_test.go`, new)

First test file in the repo. `TestWindowSizeMsgNeverPanics` drives a
`tea.WindowSizeMsg{Width:120,Height:40}` through `model.Update` on each stage:
**list** (where the first resize lands, the crash path), **form** (textarea built),
and **target** (picker built with a nil client — `buildTargets` degrades to just
the new-session target). `newTestModel()` builds a model with empty backlogs, no
socket, and `project.available()==true` (the real-launch condition).

Proven to actually catch the bug: temporarily restoring the unconditional
`applySizes` makes the `list_stage` subtest fail with the identical
`textarea.(*Model).SetWidth` ← `applySizes` panic; the fix makes it pass.

## Useful techniques discovered (for future herdr-plugin debugging)

- **Talk to herdr directly over its socket** without being a plugin:
  `printf '{"id":"x","method":"pane.list","params":{}}\n' | nc -U ~/.config/herdr/herdr.sock`
  (also `pane.read`, `pane.get`, `workspace.get`, etc.). `herdr.sock` is the API
  socket; `herdr-client.sock` is the client transport (don't use it for RPC).
- **`~/.config/herdr/herdr-server.log`** records pane spawn/exit with **exit codes
  and signals** — the fastest way to see a pane process dying and *how*.
- A herdr plugin pane's **stderr is the pty**, which is torn down on exit — so
  errors vanish. Log to a **file** to debug startup, and use
  `tea.WithoutCatchPanics()` + `recover()` + `debug.Stack()` to surface a Bubble
  Tea panic that's otherwise flattened to "program experienced a panic".
- Reproducing a herdr pane outside herdr needs a **real, sized PTY** (Bubble Tea
  resize handling differs from a pipe). A `stdin=/dev/null` repro is misleading —
  it triggers a *different* `/dev/tty` failure than the real bug.
- herdr-plus source (good reference) lives unpacked at
  `~/.config/herdr/plugins/github/cloudmanic.herdr-plus-*/`.

## State / follow-ups

- `ui.go` modified, `ui_test.go` added — **uncommitted** (repo otherwise at the
  initial commit `9166d3d`). Commit when ready.
- Optional hardening (discussed, not done): a durable `recover()` around
  `p.Run()` in `runTodoUI` that logs a panic somewhere other than the dying pty —
  any future TUI panic still silently closes the pane with exit 0.
- Pre-existing follow-ups still open from the build session: focus target pane
  after an existing-session drop; auto-mark a todo done after a "run" drop; harden
  the new-session paste readiness probe; mouse support; reorder todos.

## Develop

```sh
cd ~/projs/go/pers/herdr-todo
make build        # go build -> bin/herdr-todo
make test         # go test ./...
make relink       # rebuild + unlink + link into the running herdr
```
