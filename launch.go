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
	enc, err := ctx.encode()
	if err != nil {
		errExit("could not encode launch context:", err)
	}

	// HERDR_BIN_PATH points at the running herdr binary; it is the portable way
	// to call back into the CLI from a plugin command.
	herdr := os.Getenv("HERDR_BIN_PATH")
	if herdr == "" {
		herdr = "herdr"
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

// runTodoUI renders the manager TUI inside the zoomed pane herdr opens for the
// `todo-ui` entrypoint. It recovers the launch context, loads the project and
// global backlogs, runs the manager, and — if the user chose to drop a prompt —
// performs that drop after the program exits (so creating panes / switching
// workspaces happens once our own pane is torn down). End users never run this
// directly; herdr does, via the pane.
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

	p := tea.NewProgram(newModel(ctx, project, global, client), tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "herdr-todo:", err)
		return
	}

	if m, ok := result.(model); ok && m.action != nil {
		if err := performDrop(client, ctx, *m.action); err != nil {
			fmt.Fprintln(os.Stderr, "herdr-todo: drop failed:", err)
		}
	}
}
