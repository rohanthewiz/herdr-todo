# herdr-todo

A **prompt backlog** for [herdr](https://herdr.dev). Jot down prompts of future
work, then — when you're ready — fuzzy-pick one and **drop it straight into a
Claude Code session**: an already-running one herdr has detected, or a brand-new
one it spins up for you in the current project.

It's a herdr plugin built the same way as
[herdr-plus](https://github.com/cloudmanic/herdr-plus): a small Go binary herdr
drives via subcommands, a Bubble Tea TUI in a herdr-managed pane, and the herdr
socket API to create panes and type into them.

## What it does

- **Two backlogs.** A per-project backlog lives in `<repo>/.herdr-todo/todos.json`
  (commit it, and it travels with the repo). A global backlog lives in herdr's
  per-plugin config dir and is visible everywhere. The list groups them
  `Project` / `Global` when both have entries.
- **Manage prompts.** Add (`ctrl+a`), edit (`ctrl+e`), mark done (`ctrl+t`),
  delete (`ctrl+x`). Type to fuzzy-filter. Each prompt has an optional title and
  a multi-line body.
- **Drop into Claude Code.** Highlight a prompt, press `enter`, and pick a target:
  - **A running session** — herdr tags panes with the agent running in them, so
    every Claude Code pane (and other agents) shows up automatically.
  - **A new session** — opens a new tab in the current project's workspace and
    launches `claude`.
- **You choose submit behavior per drop.**
  - `enter` — _paste, don't run_: types the prompt into Claude Code's input but
    doesn't submit, so you can review/edit and press Enter yourself.
  - `ctrl+r` — _drop & run_: submits it so Claude starts working immediately.
- **A persistent pane.** The manager stays open after a drop, so you can fire off
  several prompts in a row. Dropping into a new session focuses that fresh Claude
  tab and leaves the manager running in the background; invoking the action again
  returns you to that same pane (per workspace) rather than spawning a duplicate.
  `esc` (or `ctrl+c`) is the way to close it.

## Install

### From GitHub

```sh
herdr plugin install rohanthewiz/herdr-todo
```

### Local development (this checkout)

```sh
herdr plugin link .
```

herdr runs the `[[build]]` step (`scripts/build.sh`, a `go build`) and registers
the `Herdr Todo: Prompts` action. After code changes:

```sh
make relink     # rebuild + unlink + link
```

Requires Go 1.26+ to build from source.

## Use

Bind a key to the **`Herdr Todo: Prompts`** action, or run it from herdr's action
menu. Inside the manager:

| Stage  | Keys                                                                                                                              |
| ------ | --------------------------------------------------------------------------------------------------------------------------------- |
| List   | `enter` drop · `ctrl+a` add · `ctrl+e` edit · `ctrl+t` toggle done · `ctrl+x` delete · type to filter · `esc` clear filter / quit |
| Form   | `tab` switch field · `ctrl+s` save · `ctrl+g` toggle Project/Global (when adding) · `esc` cancel                                  |
| Target | `enter` paste (don't submit) · `ctrl+r` drop & run · `esc` back                                                                   |

## How the "drop" works

- **Existing pane:** the prompt is sent to that pane via herdr's
  `pane.send_input` (a real Enter key in run mode; no key in paste mode).
- **New session:** a tab is created in the project workspace
  (`tab.create`), then:
  - _run mode_ launches `claude <prompt>` — Claude Code takes a leading prompt
    argument and starts working on it immediately;
  - _paste mode_ launches bare `claude`, waits for its input UI to draw, then
    types the prompt without submitting.

The drop runs off the UI thread while the manager pane stays alive, so it can
take the seconds a new session needs without freezing — and so the pane persists
for the next drop.

## Layout

```
main.go      subcommand dispatch (todo / todo-ui / version)
launch.go    the `todo` action (focuses or opens the UI pane) + the `todo-ui` runner
ui.go        the Bubble Tea manager (list / form / confirm / target stages)
store.go     project + global JSON-backed todo storage
drop.go      sending a prompt to an existing pane or a new claude session
herdr.go     herdr unix-socket JSON-RPC client (adapted from herdr-plus)
context.go   launch-context plumbing (adapted from herdr-plus)
fuzzylist.go reusable fuzzy list TUI component (adapted from herdr-plus)
styles.go    lipgloss palette
util.go      small helpers
```

## Credits

The herdr socket client, run-context plumbing, and fuzzy-list component are
adapted from [herdr-plus](https://github.com/cloudmanic/herdr-plus) by Cloudmanic
Labs, LLC, used under the MIT License. See `NOTICE`.

## License

MIT — see `LICENSE`.
