package main

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

// claudeReadyProbes are substrings that signal Claude Code's input UI has drawn
// and is ready to receive a pasted prompt. We poll the new pane for any of them
// before pasting (paste mode) so keystrokes are not dropped into a half-started
// app. Matching is best effort — on timeout we paste anyway.
var claudeReadyProbes = []string{
	"? for shortcuts",
	"for shortcuts",
	"Welcome to Claude",
	"/help for help",
	"esc to interrupt",
	"Bypassing Permissions",
}

// performDrop carries out the chosen drop after the TUI has exited. For an
// existing pane it types the prompt straight in; for a new session it opens a
// tab, launches claude, and feeds the prompt. In both cases dropRun submits with
// Enter while dropPaste leaves the text unsubmitted for the user to review.
func performDrop(client *herdrClient, ctx RunContext, act pendingAction) error {
	if client == nil {
		return errors.New("herdr socket unavailable")
	}
	prompt := act.todo.Prompt
	switch act.target.kind {
	case targetExistingPane:
		var err error
		if act.mode == dropRun {
			err = client.sendInput(act.target.paneID, prompt, "Enter")
		} else {
			err = client.sendInput(act.target.paneID, prompt)
		}
		if err != nil {
			return err
		}
		// Switch to the pane we just dropped into, mirroring how a new-session
		// drop focuses its freshly-created tab. Best effort: the prompt is
		// already delivered, so a focus failure must not fail the drop.
		focusPane(act.target.paneID)
		return nil
	case targetNewSession:
		return dropIntoNewSession(client, ctx, act, prompt)
	}
	return errors.New("unknown drop target")
}

// focusPane brings the pane with paneID to the foreground. There is no socket
// method to focus an existing pane by id (only directional focus), so we call
// back through the herdr CLI — the same path launchTodo uses to refocus the
// manager. HERDR_BIN_PATH points at the running herdr binary inside a plugin
// command. The error is returned for the caller to ignore as best effort.
func focusPane(paneID string) error {
	herdr := os.Getenv("HERDR_BIN_PATH")
	if herdr == "" {
		herdr = "herdr"
	}
	return exec.Command(herdr, "plugin", "pane", "focus", paneID).Run()
}

// dropIntoNewSession opens a fresh tab in the project's workspace, launches
// Claude Code, and delivers the prompt.
//
//   - Run mode launches `claude <prompt>` directly: Claude Code takes a leading
//     positional prompt and starts working on it immediately — the most reliable
//     way to "drop and go".
//   - Paste mode launches a bare `claude`, waits for its input UI to appear, then
//     types the prompt without submitting so the user can review and edit it.
func dropIntoNewSession(client *herdrClient, ctx RunContext, act pendingAction, prompt string) error {
	wsID, err := resolveWorkspaceID(client, ctx)
	if err != nil {
		return err
	}

	label := "claude"
	if t := firstNonEmpty(act.todo.Title, firstLine(prompt, 18)); t != "" {
		label = "claude: " + truncate(t, 18)
	}

	_, paneID, err := client.tabCreate(wsID, label, true)
	if err != nil {
		return err
	}

	if act.mode == dropRun {
		// `claude <prompt>` — launch and run in one shot.
		return client.runCommand(paneID, "claude "+shellQuote(prompt))
	}

	// Paste mode: launch bare claude, wait for it to be ready, then type the
	// prompt without submitting.
	if err := client.runCommand(paneID, "claude"); err != nil {
		return err
	}
	client.waitForPaneAnyText(paneID, claudeReadyProbes, 12*time.Second)
	return client.sendInput(paneID, prompt)
}

// resolveWorkspaceID finds the workspace to open the new session in: the one the
// todo manager was launched from, falling back to the focused pane's workspace.
func resolveWorkspaceID(client *herdrClient, ctx RunContext) (string, error) {
	if ctx.WorkspaceId != "" {
		return ctx.WorkspaceId, nil
	}
	paneID, err := client.focusedPaneID()
	if err != nil {
		return "", errors.New("could not determine a workspace for the new session")
	}
	pane, err := client.paneGet(paneID)
	if err != nil || pane.WorkspaceID == "" {
		return "", errors.New("could not determine a workspace for the new session")
	}
	return pane.WorkspaceID, nil
}
