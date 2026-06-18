package main

import (
	"encoding/base64"
	"testing"
)

// TestRunContextRoundTrip proves a context survives the encode→decode hop the
// launching action uses to hand metadata to the UI pane through one env var.
func TestRunContextRoundTrip(t *testing.T) {
	want := RunContext{
		WorkDir:        "/home/me/project",
		PaneId:         "pane-1",
		TabId:          "tab-2",
		TabLabel:       "work",
		WorkspaceId:    "ws-3",
		WorkspaceLabel: "My Project",
		Agent:          "claude",
	}
	enc, err := want.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeRunContext(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestDecodeRunContextEmptyIsZero(t *testing.T) {
	// An empty blob must yield a usable zero context, not an error — the UI still
	// runs with no metadata.
	got, err := decodeRunContext("")
	if err != nil {
		t.Fatalf("decode(\"\") = %v, want nil", err)
	}
	if got != (RunContext{}) {
		t.Errorf("decode(\"\") = %+v, want zero context", got)
	}
}

func TestDecodeRunContextErrors(t *testing.T) {
	t.Run("invalid base64", func(t *testing.T) {
		if _, err := decodeRunContext("not!valid!base64!"); err == nil {
			t.Error("decode of non-base64 should error")
		}
	})
	t.Run("valid base64 but not JSON", func(t *testing.T) {
		blob := base64.StdEncoding.EncodeToString([]byte("this is not json"))
		if _, err := decodeRunContext(blob); err == nil {
			t.Error("decode of non-JSON payload should error")
		}
	})
}

// TestContextFromPluginEnv covers the mapping from herdr's injected
// HERDR_PLUGIN_CONTEXT_JSON to a RunContext, including the documented rule that
// the focused pane's cwd is preferred over the workspace cwd.
func TestContextFromPluginEnv(t *testing.T) {
	t.Run("focused pane cwd preferred", func(t *testing.T) {
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{
			"workspace_id": "ws-1",
			"workspace_label": "Proj",
			"workspace_cwd": "/ws/root",
			"tab_id": "tab-1",
			"tab_label": "main",
			"focused_pane_id": "pane-9",
			"focused_pane_cwd": "/ws/root/sub",
			"focused_pane_agent": "claude"
		}`)
		got := contextFromPluginEnv()
		if got.WorkDir != "/ws/root/sub" {
			t.Errorf("WorkDir = %q, want the focused pane cwd", got.WorkDir)
		}
		if got.WorkspaceId != "ws-1" || got.WorkspaceLabel != "Proj" {
			t.Errorf("workspace fields = %q/%q, want ws-1/Proj", got.WorkspaceId, got.WorkspaceLabel)
		}
		if got.PaneId != "pane-9" || got.Agent != "claude" {
			t.Errorf("pane fields = %q/%q, want pane-9/claude", got.PaneId, got.Agent)
		}
	})

	t.Run("falls back to workspace cwd", func(t *testing.T) {
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_cwd": "/ws/root"}`)
		if got := contextFromPluginEnv(); got.WorkDir != "/ws/root" {
			t.Errorf("WorkDir = %q, want the workspace cwd fallback", got.WorkDir)
		}
	})

	t.Run("missing env yields zero context", func(t *testing.T) {
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
		if got := contextFromPluginEnv(); got != (RunContext{}) {
			t.Errorf("contextFromPluginEnv() = %+v, want zero context", got)
		}
	})

	t.Run("malformed JSON still launches with a zero context", func(t *testing.T) {
		// contextFromPluginEnv swallows the unmarshal error on purpose so a bad
		// payload from herdr can't stop the UI from opening.
		t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "{not json")
		if got := contextFromPluginEnv(); got != (RunContext{}) {
			t.Errorf("contextFromPluginEnv() on bad JSON = %+v, want zero context", got)
		}
	})
}
