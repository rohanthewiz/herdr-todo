package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// launchTodo is the `todo` action's entry point. herdr runs it server-side (from
// the action / keybinding), so it has no terminal of its own. It captures the
// focused pane's context, then asks herdr to open the manager UI as a zoomed
// plugin pane (the `todo-ui` entrypoint), passing the context along so project
// todos are scoped and any new Claude session opens in the project you launched
// from. herdr tears the pane down when the UI exits.
func launchTodo() {
	ctx := contextFromPluginEnv()

	// HERDR_BIN_PATH points at the running herdr binary; it is the portable way
	// to call back into the CLI from a plugin command.
	herdr := os.Getenv("HERDR_BIN_PATH")
	if herdr == "" {
		herdr = "herdr"
	}

	// Persistent pane: if a manager pane is already open in this workspace, just
	// focus it instead of spawning a duplicate. The manager survives drops (it
	// drops in-loop rather than exiting), so re-invoking the action should return
	// you to that same pane. A failed focus (the pane vanished between listing and
	// focusing) falls through to opening a fresh one.
	if paneID, ok := existingTodoPane(ctx); ok {
		focus := exec.Command(herdr, "plugin", "pane", "focus", paneID)
		focus.Stdout = os.Stdout
		focus.Stderr = os.Stderr
		if err := focus.Run(); err == nil {
			return
		}
	}

	enc, err := ctx.encode()
	if err != nil {
		errExit("could not encode launch context:", err)
	}

	args := []string{
		"plugin", "pane", "open",
		"--plugin", "rohanthewiz.herdr-todo",
		"--entrypoint", "todo-ui",
		"--placement", "zoomed",
		"--env", "HERDR_TODO_CTX=" + enc,
	}
	// No --cwd: the manifest registers the pane with a relative command
	// (./bin/herdr-todo), which herdr resolves against the plugin's install dir.
	// The launch directory reaches the UI via HERDR_TODO_CTX (ctx.WorkDir).

	cmd := exec.Command(herdr, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		errExit("could not open the todo manager:", err)
	}
}

// paneTitle is the human label herdr gives the manager pane (the `todo-ui`
// entry's title in herdr-plugin.toml). herdr reports it on pane.list as both the
// pane's Title and Label, so we match on it to recognize an already-open manager.
const paneTitle = "Herdr Todo"

// existingTodoPane finds an already-open manager pane to reuse, returning its id.
// It matches our manifest pane title and, when the launch context names a
// workspace, restricts the match to that workspace so each project keeps its own
// correctly-scoped manager (project todos are scoped to where the pane opened).
// It returns ("", false) when the herdr socket is unavailable or nothing matches,
// in which case the caller opens a fresh pane.
func existingTodoPane(ctx RunContext) (string, bool) {
	client, err := newHerdrClient()
	if err != nil {
		return "", false
	}
	panes, err := client.paneList()
	if err != nil {
		return "", false
	}
	for _, p := range panes {
		if p.Title != paneTitle && p.Label != paneTitle {
			continue
		}
		if ctx.WorkspaceId != "" && p.WorkspaceID != "" && p.WorkspaceID != ctx.WorkspaceId {
			continue
		}
		return p.PaneID, true
	}
	return "", false
}

// runTodoUI renders the manager TUI inside the zoomed pane herdr opens for the
// `todo-ui` entrypoint. It recovers the launch context, loads the project and
// global backlogs, and runs the manager. Drops happen in-loop, off the UI thread
// (see chooseTarget), so the pane persists across them; Run() only returns when
// the user quits. End users never run this directly; herdr does, via the pane.
func runTodoUI() {
	ctx, err := decodeRunContext(os.Getenv("HERDR_TODO_CTX"))
	if err != nil {
		errExit("could not decode launch context:", err)
	}

	// When launched directly (e.g. as a herdr-plus project tab command) rather
	// than through the `todo` action, HERDR_TODO_CTX is absent and ctx is empty.
	// Fall back to the pane's working directory so project todos still scope to
	// the directory the pane opened in.
	if ctx.WorkDir == "" {
		if wd, e := os.Getwd(); e == nil {
			ctx.WorkDir = wd
		}
	}

	project, global, err := loadStores(ctx)
	if err != nil {
		// Leave the pane open so the user can read the error.
		errExit(err)
	}

	// The socket client drives session drops; the manager still works without it
	// (you can add/edit/organize prompts), so a missing socket is not fatal here.
	client, _ := newHerdrClient()

	// The manager performs drops itself, in-loop, so the pane persists across
	// drops (see chooseTarget). Run() only returns when the user quits, at which
	// point herdr tears the pane down — the deliberate way to close the manager.
	p := tea.NewProgram(newModel(ctx, project, global, client), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-todo:", err)
		return
	}
}
