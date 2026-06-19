package main

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newModelInTemp builds a manager whose project and global backlogs are backed
// by fresh files under t.TempDir(), so form saves, toggles, and deletes actually
// persist somewhere isolated and auto-cleaned. The project store has a path, so
// available() is true — the in-project launch where scope defaults matter.
func newModelInTemp(t *testing.T) (model, *store, *store) {
	t.Helper()
	dir := t.TempDir()
	project := &store{scope: scopeProject, path: filepath.Join(dir, "project", "todos.json")}
	global := &store{scope: scopeGlobal, path: filepath.Join(dir, "global", "todos.json")}
	m := newModel(RunContext{WorkDir: filepath.Join(dir, "project")}, project, global, nil)
	return m, project, global
}

// TestSaveFormAddsTodo walks the add flow the way a keypress would: beginAdd
// enters the form, the prompt gets text, saveForm persists. It checks the todo
// reaches both the in-memory store and disk, that a blank title is derived from
// the prompt's first line, and that the UI returns to the list.
func TestSaveFormAddsTodo(t *testing.T) {
	m, project, _ := newModelInTemp(t)

	next, _ := m.beginAdd()
	m = next.(model)
	if m.stage != stageForm {
		t.Fatalf("beginAdd stage = %v, want stageForm", m.stage)
	}
	// In a project, a new todo defaults to the project backlog.
	if m.formScope != scopeProject {
		t.Errorf("formScope = %v, want scopeProject in an available project", m.formScope)
	}

	// Leave the title blank so it gets derived from the prompt's first line.
	m.promptArea.SetValue("Wire up the export button\nplus follow-up details")
	next, _ = m.saveForm()
	m = next.(model)

	if m.stage != stageList {
		t.Fatalf("after save stage = %v, want stageList", m.stage)
	}
	if len(project.todos) != 1 {
		t.Fatalf("project has %d todos, want 1", len(project.todos))
	}
	if got := project.todos[0].Title; got != "Wire up the export button" {
		t.Errorf("derived title = %q, want the prompt's first line", got)
	}

	// Confirm it persisted, not just mutated in memory.
	reloaded := &store{scope: scopeProject, path: project.path}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.todos) != 1 || reloaded.todos[0].Prompt != "Wire up the export button\nplus follow-up details" {
		t.Errorf("disk todos = %+v, want the saved prompt", reloaded.todos)
	}
}

func TestSaveFormRejectsEmptyPrompt(t *testing.T) {
	m, project, _ := newModelInTemp(t)
	next, _ := m.beginAdd()
	m = next.(model)

	// Only whitespace in the prompt — saveForm should refuse and stay on the form.
	m.promptArea.SetValue("   \n  ")
	next, _ = m.saveForm()
	m = next.(model)

	if m.stage != stageForm {
		t.Errorf("stage = %v, want to stay on stageForm for an empty prompt", m.stage)
	}
	if m.formErr == "" {
		t.Error("expected a formErr for an empty prompt")
	}
	if len(project.todos) != 0 {
		t.Errorf("an empty prompt added %d todos, want 0", len(project.todos))
	}
}

// TestBeginAddDefaultsToGlobalOutsideProject pins the scope default: with no
// project store available, a new todo can only go to the global backlog.
func TestBeginAddDefaultsToGlobalOutsideProject(t *testing.T) {
	project := &store{scope: scopeProject, path: ""} // unavailable: no project
	global := &store{scope: scopeGlobal, path: filepath.Join(t.TempDir(), "todos.json")}
	m := newModel(RunContext{}, project, global, nil)

	next, _ := m.beginAdd()
	m = next.(model)
	if m.formScope != scopeGlobal {
		t.Errorf("formScope = %v, want scopeGlobal when no project is available", m.formScope)
	}
}

// TestToggleSelected flips the highlighted todo's done flag and reports the right
// status, both directions.
func TestToggleSelected(t *testing.T) {
	m, project, _ := newModelInTemp(t)
	if err := project.add(Todo{ID: "x", Title: "task", Prompt: "do it"}); err != nil {
		t.Fatal(err)
	}
	m.rebuildList() // pick up the new todo and park the cursor on it

	next, _ := m.toggleSelected()
	m = next.(model)
	if got, _ := project.find("x"); !got.Done {
		t.Error("toggleSelected did not mark the todo done")
	}
	if m.status != "marked done" {
		t.Errorf("status = %q, want 'marked done'", m.status)
	}

	next, _ = m.toggleSelected()
	m = next.(model)
	if got, _ := project.find("x"); got.Done {
		t.Error("second toggleSelected did not reopen the todo")
	}
	if m.status != "reopened" {
		t.Errorf("status = %q, want 'reopened'", m.status)
	}
}

// TestDeleteConfirmFlow runs the two-step delete: beginDelete arms the confirm
// stage, then a "y" key carries it out and returns to the list.
func TestDeleteConfirmFlow(t *testing.T) {
	m, project, _ := newModelInTemp(t)
	if err := project.add(Todo{ID: "gone", Title: "remove me", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	m.rebuildList()

	next, _ := m.beginDelete()
	m = next.(model)
	if m.stage != stageConfirm {
		t.Fatalf("beginDelete stage = %v, want stageConfirm", m.stage)
	}
	if m.pendingTitle != "remove me" {
		t.Errorf("pendingTitle = %q, want 'remove me'", m.pendingTitle)
	}

	next, _ = m.updateConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(model)
	if m.stage != stageList {
		t.Errorf("after confirm stage = %v, want stageList", m.stage)
	}
	if _, ok := project.find("gone"); ok {
		t.Error("confirmed delete did not remove the todo")
	}
	if m.status != "deleted" {
		t.Errorf("status = %q, want 'deleted'", m.status)
	}
}

// TestDeleteConfirmCancel pins that answering "n" keeps the todo.
func TestDeleteConfirmCancel(t *testing.T) {
	m, project, _ := newModelInTemp(t)
	if err := project.add(Todo{ID: "keep", Title: "keep me", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	m.rebuildList()

	next, _ := m.beginDelete()
	m = next.(model)
	next, _ = m.updateConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(model)
	if m.stage != stageList {
		t.Errorf("after cancel stage = %v, want stageList", m.stage)
	}
	if _, ok := project.find("keep"); !ok {
		t.Error("cancelling the confirm deleted the todo anyway")
	}
}

// hasHeading reports whether the rebuilt list contains a non-selectable group
// heading with the given name.
func hasHeading(items []listItem, name string) bool {
	for _, it := range items {
		if !it.selectable && it.name == name {
			return true
		}
	}
	return false
}

// TestRebuildListGroupsOnlyWhenBothScopesHaveTodos covers the grouping rule:
// "Project"/"Global" headings appear only when both backlogs are non-empty.
func TestRebuildListGroupsOnlyWhenBothScopesHaveTodos(t *testing.T) {
	t.Run("both populated shows headings", func(t *testing.T) {
		m, project, global := newModelInTemp(t)
		if err := project.add(Todo{ID: "p", Prompt: "proj todo"}); err != nil {
			t.Fatal(err)
		}
		if err := global.add(Todo{ID: "g", Prompt: "glob todo"}); err != nil {
			t.Fatal(err)
		}
		m.rebuildList()
		if !hasHeading(m.list.items, "Project") || !hasHeading(m.list.items, "Global") {
			t.Errorf("expected both group headings; items = %+v", m.list.items)
		}
		if len(m.rows) != 2 {
			t.Errorf("rows = %d, want 2 selectable todos", len(m.rows))
		}
	})

	t.Run("single scope shows no headings", func(t *testing.T) {
		m, _, global := newModelInTemp(t)
		if err := global.add(Todo{ID: "g", Prompt: "glob only"}); err != nil {
			t.Fatal(err)
		}
		m.rebuildList()
		if hasHeading(m.list.items, "Project") || hasHeading(m.list.items, "Global") {
			t.Errorf("expected no headings with a single populated scope; items = %+v", m.list.items)
		}
	})
}

// TestChooseTargetStaysOpen pins the persistent-pane behavior: choosing a drop
// target no longer quits the program. Instead it returns to the list, marks a
// drop in flight, shows a "dropping…" status, and hands back a command that will
// perform the drop off the UI thread. The pane lives on so more prompts can drop.
func TestChooseTargetStaysOpen(t *testing.T) {
	m, project, _ := newModelInTemp(t)
	if err := project.add(Todo{ID: "d", Prompt: "drop me"}); err != nil {
		t.Fatal(err)
	}
	m.rebuildList()

	// Stand up the target picker the way beginDrop would, but without a socket:
	// buildTargets degrades to just the new-session target (selected by default).
	m.dropTodo = todoRef{scope: scopeProject, id: "d"}
	m.targets, m.targetList = m.buildTargets()
	m.stage = stageTarget

	next, cmd := m.chooseTarget(dropPaste)
	m = next.(model)
	if m.quitting {
		t.Error("chooseTarget set quitting; the persistent pane must stay open")
	}
	if !m.dropping {
		t.Error("chooseTarget did not mark a drop in flight")
	}
	if m.stage != stageList {
		t.Errorf("stage = %v, want stageList after starting a drop", m.stage)
	}
	if m.status == "" || m.statusErr {
		t.Errorf("expected a non-error 'dropping…' status; got status=%q err=%v", m.status, m.statusErr)
	}
	if cmd == nil {
		t.Fatal("chooseTarget returned no command to perform the drop")
	}

	// The command performs the drop and reports back. With a nil socket the drop
	// fails, but the result still flows through Update, which clears the in-flight
	// flag and surfaces the error — leaving the manager usable.
	msg := cmd()
	res, ok := msg.(dropResultMsg)
	if !ok {
		t.Fatalf("drop command returned %T, want dropResultMsg", msg)
	}
	if res.err == nil {
		t.Error("expected the socket-less drop to fail")
	}
	next, _ = m.Update(res)
	m = next.(model)
	if m.dropping {
		t.Error("dropResultMsg did not clear the in-flight flag")
	}
	if !m.statusErr {
		t.Errorf("expected an error status after a failed drop; got status=%q", m.status)
	}
}

// TestBeginDropWhileDroppingIsRejected pins that a second drop can't start while
// one is still in flight — the guard that keeps two performDrop goroutines from
// racing on the same manager.
func TestBeginDropWhileDroppingIsRejected(t *testing.T) {
	m, project, _ := newModelInTemp(t)
	if err := project.add(Todo{ID: "d", Prompt: "drop me"}); err != nil {
		t.Fatal(err)
	}
	m.rebuildList()
	m.dropping = true

	next, _ := m.beginDrop()
	m = next.(model)
	if m.stage != stageList {
		t.Errorf("stage = %v, want to stay on stageList while a drop is in flight", m.stage)
	}
	if m.status == "" || m.statusErr {
		t.Errorf("expected an informational 'in progress' status; got status=%q err=%v", m.status, m.statusErr)
	}
}

// TestTargetDesc covers the short destination labels used in the status line.
func TestTargetDesc(t *testing.T) {
	if got := targetDesc(dropTarget{kind: targetNewSession}); got != "new Claude Code session" {
		t.Errorf("new-session desc = %q", got)
	}
	if got := targetDesc(dropTarget{kind: targetExistingPane, agent: "claude"}); got != "claude" {
		t.Errorf("existing-pane desc = %q, want the agent name", got)
	}
	if got := targetDesc(dropTarget{kind: targetExistingPane}); got != "session" {
		t.Errorf("agentless existing-pane desc = %q, want the 'session' fallback", got)
	}
}

// TestBeginDropWithoutSocketReportsError pins that dropping with no herdr socket
// surfaces a status error instead of advancing to the (unusable) target picker.
func TestBeginDropWithoutSocketReportsError(t *testing.T) {
	m, project, _ := newModelInTemp(t)
	if err := project.add(Todo{ID: "d", Prompt: "drop me"}); err != nil {
		t.Fatal(err)
	}
	m.rebuildList()

	next, _ := m.beginDrop()
	m = next.(model)
	if m.stage != stageList {
		t.Errorf("stage = %v, want to stay on stageList without a socket", m.stage)
	}
	if !m.statusErr || m.status == "" {
		t.Errorf("expected an error status; got status=%q err=%v", m.status, m.statusErr)
	}
}
