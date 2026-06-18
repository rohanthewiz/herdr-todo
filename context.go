// context.go — the launch context herdr-todo carries from the action (which
// runs server-side) to the manager UI (which runs in a pane).
//
// Adapted from herdr-plus (https://github.com/cloudmanic/herdr-plus),
// Copyright (c) 2026 Cloudmanic Labs, LLC, MIT License. See NOTICE.

package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
)

// RunContext describes where herdr-todo was launched from: the focused pane's
// working directory and the workspace/tab it lived in. It is gathered when the
// `todo` action fires, then serialized and handed to the manager UI pane. The UI
// uses WorkDir to scope project todos and to root any new Claude session, and
// WorkspaceId to create that session in the project you launched from.
type RunContext struct {
	WorkDir        string `json:"work_dir"`
	PaneId         string `json:"pane_id"`
	TabId          string `json:"tab_id"`
	TabLabel       string `json:"tab_label"`
	WorkspaceId    string `json:"workspace_id"`
	WorkspaceLabel string `json:"workspace_label"`
	Agent          string `json:"agent"`
}

// encode serializes the context to a base64 JSON blob so the launching action
// can pass it to the UI pane as a single, shell-safe environment variable.
func (c RunContext) encode() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// decodeRunContext is the inverse of encode. An empty string yields a zero
// context rather than an error so the UI can still run with no metadata.
func decodeRunContext(s string) (RunContext, error) {
	var c RunContext
	if s == "" {
		return c, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

// pluginContext mirrors the subset of HERDR_PLUGIN_CONTEXT_JSON herdr-todo reads.
// herdr injects this when it runs a plugin action, describing the pane that was
// focused when the action fired.
type pluginContext struct {
	WorkspaceID      string `json:"workspace_id"`
	WorkspaceLabel   string `json:"workspace_label"`
	WorkspaceCwd     string `json:"workspace_cwd"`
	TabID            string `json:"tab_id"`
	TabLabel         string `json:"tab_label"`
	FocusedPaneID    string `json:"focused_pane_id"`
	FocusedPaneCwd   string `json:"focused_pane_cwd"`
	FocusedPaneAgent string `json:"focused_pane_agent"`
}

// contextFromPluginEnv builds a RunContext from HERDR_PLUGIN_CONTEXT_JSON, which
// herdr sets when it runs the todo action. The working directory is the focused
// pane's cwd (the user's real directory), falling back to the workspace cwd. Any
// field herdr does not supply is left empty — a partial context still launches.
func contextFromPluginEnv() RunContext {
	var pc pluginContext
	if raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &pc)
	}
	return RunContext{
		WorkDir:        firstNonEmpty(pc.FocusedPaneCwd, pc.WorkspaceCwd),
		PaneId:         pc.FocusedPaneID,
		TabId:          pc.TabID,
		TabLabel:       pc.TabLabel,
		WorkspaceId:    pc.WorkspaceID,
		WorkspaceLabel: pc.WorkspaceLabel,
		Agent:          pc.FocusedPaneAgent,
	}
}
