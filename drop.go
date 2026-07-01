package main

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

// claudeReadyProbes are substrings that signal Claude Code's input UI has drawn
// and is ready to receive a pasted prompt. We poll the new pane for any of them
// before pasting so keystrokes are not dropped into a half-started app.
// Matching is best effort — on timeout we paste anyway. These track Claude
// Code's footer/banner strings and may need refreshing as its UI evolves.
var claudeReadyProbes = []string{
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

// dropIntoNewSession opens a fresh tab in the project's workspace, launches the
// target's agent (claude by default), waits for its input UI, and delivers the
// prompt as typed input. Run mode adds a real Enter so the agent starts working;
// paste mode stops short so the user can review and edit. One delivery path for
// both modes — and for any agent — with no shell quoting to get wrong and no
// prompt leaking into shell history or `ps` output.
func dropIntoNewSession(client *herdrClient, ctx RunContext, act pendingAction, prompt string) error {
	wsID, err := resolveWorkspaceID(client, ctx)
	if err != nil {
		return err
	}

	command := firstNonEmpty(act.target.command, "claude")
	label := command
	if t := firstNonEmpty(act.todo.Title, firstLine(prompt, 18)); t != "" {
		label = command + ": " + truncate(t, 18)
	}

	_, paneID, err := client.tabCreate(wsID, label, true)
	if err != nil {
		return err
	}

	if err := client.runCommand(paneID, command); err != nil {
		return err
	}
	waitForAgentReady(client, paneID, command)
	if act.mode == dropRun {
		return client.sendInput(paneID, prompt, "Enter")
	}
	return client.sendInput(paneID, prompt)
}

// waitForAgentReady blocks until a freshly launched agent looks ready to accept
// a pasted prompt. Claude Code has known footer/banner strings to probe for;
// other agents get a short fixed grace period instead. Best effort either way —
// on timeout we paste anyway.
func waitForAgentReady(client *herdrClient, paneID, command string) {
	if command == "claude" {
		client.waitForPaneAnyText(paneID, claudeReadyProbes, 12*time.Second)
		return
	}
	time.Sleep(2500 * time.Millisecond)
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
