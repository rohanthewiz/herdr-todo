// herdr.go — a small client for herdr's plugin socket API.
//
// Adapted from herdr-plus (https://github.com/cloudmanic/herdr-plus),
// Copyright (c) 2026 Cloudmanic Labs, LLC, MIT License. See NOTICE.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// herdrClient talks to the running herdr instance over its unix domain socket.
// The protocol is newline-delimited JSON: one request object per line, one
// response object per line. Each call opens a short-lived connection, writes a
// single request, and reads a single response. herdr injects HERDR_SOCKET_PATH
// into every plugin command, so this works whenever herdr runs us.
type herdrClient struct {
	socketPath string
}

// newHerdrClient builds a client from the HERDR_SOCKET_PATH environment
// variable. It returns an error when the process is not running inside herdr.
func newHerdrClient() (*herdrClient, error) {
	path := os.Getenv("HERDR_SOCKET_PATH")
	if path == "" {
		return nil, errors.New("HERDR_SOCKET_PATH is not set; are you running inside herdr?")
	}
	return &herdrClient{socketPath: path}, nil
}

type request struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type herdrError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *herdrError     `json:"error"`
}

// call sends a single request over a fresh connection and decodes the result
// into out (which may be nil when the caller does not care about the payload).
func (c *herdrClient) call(method string, params map[string]any, out any) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("connect herdr socket: %w", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(request{ID: "herdr-todo", Method: method, Params: params}); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	var resp response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("herdr error %s: %s", resp.Error.Code, resp.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

// paneInfo is the subset of herdr's pane metadata (PaneInfo in herdr's schema)
// that herdr-todo reads. herdr returns the same shape from pane.list and
// pane.get, including the per-pane Agent label ("claude", "codex", …) — which
// is how we find Claude Code sessions to drop a prompt into.
type paneInfo struct {
	PaneID        string `json:"pane_id"`
	TerminalID    string `json:"terminal_id"`
	WorkspaceID   string `json:"workspace_id"`
	TabID         string `json:"tab_id"`
	Focused       bool   `json:"focused"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
	Label         string `json:"label"`
	Title         string `json:"title"`
	Agent         string `json:"agent"`
	DisplayAgent  string `json:"display_agent"`
}

// paneList returns every pane herdr currently knows about (across all
// workspaces). Each entry carries its Agent label and workspace/tab ids.
func (c *herdrClient) paneList() ([]paneInfo, error) {
	var out struct {
		Panes []paneInfo `json:"panes"`
	}
	if err := c.call("pane.list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Panes, nil
}

// paneGet fetches metadata for a single pane.
func (c *herdrClient) paneGet(paneID string) (paneInfo, error) {
	var out struct {
		Pane paneInfo `json:"pane"`
	}
	err := c.call("pane.get", map[string]any{"pane_id": paneID}, &out)
	return out.Pane, err
}

// focusedPaneID returns the id of the currently focused pane.
func (c *herdrClient) focusedPaneID() (string, error) {
	panes, err := c.paneList()
	if err != nil {
		return "", err
	}
	for _, p := range panes {
		if p.Focused {
			return p.PaneID, nil
		}
	}
	return "", errors.New("no focused pane")
}

// sendInput types text into a pane and then presses the given keys, as if at the
// keyboard. To RUN a command, pass the text and "Enter" as the sole key — do not
// embed a trailing newline in text. herdr's send_input treats text as a paste:
// once the shell/agent line editor is active it inserts an embedded "\n"
// literally instead of submitting, so the line would just sit there until the
// user pressed Enter by hand. A real Enter key always submits. Pass no keys for
// plain typing with no submission.
func (c *herdrClient) sendInput(paneID, text string, keys ...string) error {
	params := map[string]any{
		"pane_id": paneID,
		"text":    text,
	}
	if len(keys) > 0 {
		params["keys"] = keys
	}
	return c.call("pane.send_input", params, nil)
}

// paneRead returns the text currently shown in a pane. source selects which
// slice of the terminal to read ("visible" for on-screen rows, "recent" for
// recent scrollback); lines caps how many trailing lines come back.
func (c *herdrClient) paneRead(paneID, source string, lines int) (string, error) {
	var out struct {
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	err := c.call("pane.read", map[string]any{
		"pane_id": paneID,
		"source":  source,
		"lines":   lines,
	}, &out)
	if err != nil {
		return "", err
	}
	return out.Read.Text, nil
}

// paneSplit splits the target pane in the given direction ("down"/"right"),
// returning the new pane's id. When focus is true the new pane becomes focused.
func (c *herdrClient) paneSplit(targetPaneID, direction string, focus bool) (string, error) {
	var out struct {
		Pane struct {
			PaneID string `json:"pane_id"`
		} `json:"pane"`
	}
	err := c.call("pane.split", map[string]any{
		"target_pane_id": targetPaneID,
		"direction":      direction,
		"focus":          focus,
	}, &out)
	if err != nil {
		return "", err
	}
	return out.Pane.PaneID, nil
}

// tabCreate adds a tab to an existing workspace and returns the new tab's id and
// its root pane's id.
func (c *herdrClient) tabCreate(workspaceID, label string, focus bool) (tabID, paneID string, err error) {
	var out struct {
		Tab struct {
			TabID string `json:"tab_id"`
		} `json:"tab"`
		RootPane struct {
			PaneID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	err = c.call("tab.create", map[string]any{
		"workspace_id": workspaceID,
		"label":        label,
		"focus":        focus,
	}, &out)
	if err != nil {
		return "", "", err
	}
	return out.Tab.TabID, out.RootPane.PaneID, nil
}

// tabRename changes a tab's human label.
func (c *herdrClient) tabRename(tabID, label string) error {
	return c.call("tab.rename", map[string]any{
		"tab_id": tabID,
		"label":  label,
	}, nil)
}

// workspaceInfo is the subset of herdr's workspace metadata herdr-todo uses.
type workspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Cwd         string `json:"cwd"`
}

// workspaceGet fetches metadata for a single workspace, notably its label.
func (c *herdrClient) workspaceGet(workspaceID string) (workspaceInfo, error) {
	var out struct {
		Workspace workspaceInfo `json:"workspace"`
	}
	err := c.call("workspace.get", map[string]any{"workspace_id": workspaceID}, &out)
	return out.Workspace, err
}

// runCommand types command into a freshly created pane and submits it, pacing
// itself to the shell's startup so the command actually runs instead of sitting
// unsubmitted at the prompt. It waits for the shell prompt to draw, types the
// command (no trailing newline), waits for the command to echo, then submits
// with a real Enter key. Every wait is best effort: on timeout it proceeds.
func (c *herdrClient) runCommand(paneID, command string) error {
	c.waitForPaneReady(paneID, 5*time.Second)
	if err := c.sendInput(paneID, command); err != nil {
		return err
	}
	c.waitForPaneText(paneID, commandEchoProbe(command), 5*time.Second)
	return c.sendInput(paneID, "", "Enter")
}

// commandEchoProbe returns a short, stable leading fragment of a command to look
// for when confirming it was typed at the prompt. A long command wraps across
// rows, so a short leading fragment is more reliable to match.
func commandEchoProbe(command string) string {
	probe := command
	if i := strings.IndexByte(probe, '\n'); i >= 0 {
		probe = probe[:i]
	}
	if len(probe) > 12 {
		probe = probe[:12]
	}
	return strings.TrimSpace(probe)
}

// waitForPaneReady blocks until the pane shows any non-blank content — its shell
// prompt — or the timeout elapses. Best effort: a timeout just stops waiting.
func (c *herdrClient) waitForPaneReady(paneID string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, err := c.paneRead(paneID, "visible", 5); err == nil && strings.TrimSpace(text) != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForPaneText blocks until the pane's visible text contains probe or the
// timeout elapses. An empty probe returns immediately. Best effort.
func (c *herdrClient) waitForPaneText(paneID, probe string, timeout time.Duration) {
	if probe == "" {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, err := c.paneRead(paneID, "visible", 40); err == nil && strings.Contains(text, probe) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForPaneAnyText blocks until the pane's visible text contains any of the
// probes, returning the matched probe (or "" on timeout). Best effort.
func (c *herdrClient) waitForPaneAnyText(paneID string, probes []string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, err := c.paneRead(paneID, "visible", 60); err == nil {
			for _, p := range probes {
				if p != "" && strings.Contains(text, p) {
					return p
				}
			}
		}
		time.Sleep(75 * time.Millisecond)
	}
	return ""
}
