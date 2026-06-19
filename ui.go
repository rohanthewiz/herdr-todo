package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// uiStage is which screen the manager is currently showing.
type uiStage int

const (
	stageList    uiStage = iota // the project+global todo list
	stageForm                   // add / edit a prompt
	stageConfirm                // confirm a delete
	stageTarget                 // pick where to drop the chosen prompt
)

// formMode distinguishes adding a new todo from editing an existing one.
type formMode int

const (
	formAdd formMode = iota
	formEdit
)

// dropTargetKind is the two ways a prompt can land: into a brand-new Claude Code
// session, or into an already-running agent pane.
type dropTargetKind int

const (
	targetNewSession dropTargetKind = iota
	targetExistingPane
)

// dropTarget is one selectable destination in the target picker.
type dropTarget struct {
	kind   dropTargetKind
	paneID string // for targetExistingPane
	agent  string // detected agent label, for existing panes
	label  string
	desc   string
}

// dropMode is the per-drop submit choice.
type dropMode int

const (
	dropPaste dropMode = iota // type the prompt but don't press Enter
	dropRun                   // type the prompt and submit it
)

// todoRef identifies a todo by its scope and id, so the list can map a row back
// to the right store.
type todoRef struct {
	scope scope
	id    string
}

// pendingAction is the drop the user chose. The manager performs it without
// quitting — off the UI thread (see performDropCmd) — so the pane persists and
// you can drop more prompts in the same session.
type pendingAction struct {
	todo   Todo
	target dropTarget
	mode   dropMode
}

// dropResultMsg reports the outcome of an asynchronous drop back to the Update
// loop, so the manager can clear its "dropping…" state and show where the prompt
// landed (or why it failed) while staying open.
type dropResultMsg struct {
	desc string // human description of the destination, for the status line
	err  error
}

// model is the Bubble Tea state for the whole manager: a small stage machine
// over the list, the add/edit form, the delete confirm, and the target picker.
type model struct {
	ctx     RunContext
	project *store
	global  *store
	client  *herdrClient // nil when the herdr socket is unavailable

	stage uiStage

	// List stage.
	list fuzzyList
	rows []todoRef // selectable row index -> todo

	// Form stage.
	formMode   formMode
	formScope  scope
	editID     string
	titleInput textinput.Model
	promptArea textarea.Model
	formFocus  int // 0 = title, 1 = prompt
	formErr    string

	// Confirm stage.
	pendingDelete todoRef
	pendingTitle  string

	// Target stage.
	dropTodo   todoRef
	targets    []dropTarget
	targetList fuzzyList

	width, height int

	status    string // transient message under the list
	statusErr bool

	dropping bool // a drop is in flight (off the UI thread); guards re-entry
	quitting bool
}

// newModel builds the initial manager state showing the todo list.
func newModel(ctx RunContext, project, global *store, client *herdrClient) model {
	m := model{ctx: ctx, project: project, global: global, client: client, stage: stageList}
	m.list = newFuzzyList("Type to filter prompts…", nil)
	m.rebuildList()
	return m
}

// Init starts the cursor blinking.
func (m model) Init() tea.Cmd { return textinput.Blink }

// Update routes by stage; non-key messages flow to whatever input is active so
// cursors keep blinking and text keeps flowing.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applySizes()
		return m, nil
	case dropResultMsg:
		m.dropping = false
		if msg.err != nil {
			m.setStatus("drop failed: "+msg.err.Error(), true)
		} else {
			m.setStatus("dropped → "+msg.desc, false)
		}
		return m, nil
	case tea.KeyMsg:
		switch m.stage {
		case stageList:
			return m.updateList(msg)
		case stageForm:
			return m.updateForm(msg)
		case stageConfirm:
			return m.updateConfirm(msg)
		case stageTarget:
			return m.updateTarget(msg)
		}
	}
	return m.forward(msg)
}

// forward passes a non-key message to the active input.
func (m model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.stage {
	case stageList:
		cmd = m.list.editQuery(msg)
	case stageTarget:
		cmd = m.targetList.editQuery(msg)
	case stageForm:
		return m.forwardForm(msg)
	}
	return m, cmd
}

// --- List stage ---------------------------------------------------------------

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		// Clear an active filter first; quit only when there's nothing to clear.
		if strings.TrimSpace(m.list.input.Value()) != "" {
			m.list.input.SetValue("")
			m.list.filter()
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "up", "ctrl+p":
		m.list.moveUp()
		return m, nil
	case "down", "ctrl+n":
		m.list.moveDown()
		return m, nil
	case "enter":
		return m.beginDrop()
	case "ctrl+a":
		return m.beginAdd()
	case "ctrl+e":
		return m.beginEdit()
	case "ctrl+x":
		return m.beginDelete()
	case "ctrl+t":
		return m.toggleSelected()
	}
	cmd := m.list.editQuery(msg)
	return m, cmd
}

// selectedRef returns the highlighted todo's ref, and whether one is selected.
func (m model) selectedRef() (todoRef, bool) {
	idx := m.list.selectedIndex()
	if idx < 0 || idx >= len(m.rows) {
		return todoRef{}, false
	}
	return m.rows[idx], true
}

func (m *model) storeFor(s scope) *store {
	if s == scopeProject {
		return m.project
	}
	return m.global
}

func (m model) resolve(ref todoRef) (Todo, bool) {
	return m.storeFor(ref.scope).find(ref.id)
}

func (m model) toggleSelected() (tea.Model, tea.Cmd) {
	ref, ok := m.selectedRef()
	if !ok {
		return m, nil
	}
	td, _ := m.resolve(ref)
	if err := m.storeFor(ref.scope).toggle(ref.id); err != nil {
		m.setStatus("save failed: "+err.Error(), true)
		return m, nil
	}
	if td.Done {
		m.setStatus("reopened", false)
	} else {
		m.setStatus("marked done", false)
	}
	m.rebuildList()
	return m, nil
}

func (m *model) setStatus(s string, isErr bool) {
	m.status = s
	m.statusErr = isErr
}

// rebuildList regenerates the list rows from the two stores. Project todos come
// first; the "Project"/"Global" group headings only show when both groups have
// at least one todo (matching how the rest of herdr-plus groups things).
func (m *model) rebuildList() {
	var items []listItem
	var rows []todoRef

	grouped := len(m.project.todos) > 0 && len(m.global.todos) > 0

	add := func(s *store) {
		for _, t := range s.todos {
			ref := todoRef{scope: s.scope, id: t.ID}
			badge := "○"
			if t.Done {
				badge = checkStyle.Render("✓")
			}
			name := t.Title
			if name == "" {
				name = firstLine(t.Prompt, 60)
			}
			items = append(items, listItem{
				name:       name,
				desc:       firstLine(t.Prompt, 70),
				badge:      badge,
				strike:     t.Done,
				selectable: true,
				ref:        len(rows),
			})
			rows = append(rows, ref)
		}
	}

	if grouped {
		items = append(items, listItem{name: "Project"})
	}
	add(m.project)
	if grouped {
		items = append(items, listItem{name: "Global"})
	}
	add(m.global)

	m.rows = rows
	// Swap in the new rows while keeping the existing query box and cursor
	// (setItems re-filters and clamps), so an add/edit/toggle doesn't disturb
	// what the user has typed or where they were.
	m.list.setItems(items)
}

// --- Add / Edit form ----------------------------------------------------------

func (m model) beginAdd() (tea.Model, tea.Cmd) {
	m.formMode = formAdd
	// Default to the project backlog when launched inside a project, else global.
	if m.project.available() {
		m.formScope = scopeProject
	} else {
		m.formScope = scopeGlobal
	}
	m.editID = ""
	m.titleInput, m.promptArea = m.newFormInputs("", "")
	m.formFocus = 1 // start in the prompt — that's the point of an entry
	m.titleInput.Blur()
	cmd := m.promptArea.Focus()
	m.formErr = ""
	m.stage = stageForm
	return m, cmd
}

func (m model) beginEdit() (tea.Model, tea.Cmd) {
	ref, ok := m.selectedRef()
	if !ok {
		return m, nil
	}
	td, ok := m.resolve(ref)
	if !ok {
		return m, nil
	}
	m.formMode = formEdit
	m.formScope = ref.scope
	m.editID = ref.id
	m.titleInput, m.promptArea = m.newFormInputs(td.Title, td.Prompt)
	m.formFocus = 1
	m.titleInput.Blur()
	cmd := m.promptArea.Focus()
	m.formErr = ""
	m.stage = stageForm
	return m, cmd
}

// newFormInputs builds the title field and prompt editor, sized to the screen
// and pre-filled with the given values.
func (m model) newFormInputs(title, prompt string) (textinput.Model, textarea.Model) {
	ti := textinput.New()
	ti.Placeholder = "Short title (optional — derived from the prompt if blank)"
	ti.Prompt = ""
	ti.CharLimit = 140
	ti.SetValue(title)

	ta := textarea.New()
	ta.Placeholder = "The prompt to hand Claude Code later…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetValue(prompt)

	w := m.width - 4
	if w < 20 {
		w = 60
	}
	ti.Width = w
	ta.SetWidth(w)
	h := m.height - 12
	if h < 4 {
		h = 8
	}
	ta.SetHeight(h)
	return ti, ta
}

func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.stage = stageList
		m.formErr = ""
		return m, nil
	case "tab", "shift+tab":
		return m.toggleFormFocus()
	case "ctrl+s":
		return m.saveForm()
	case "ctrl+g":
		if m.formMode == formAdd && m.project.available() {
			if m.formScope == scopeProject {
				m.formScope = scopeGlobal
			} else {
				m.formScope = scopeProject
			}
		}
		return m, nil
	}
	return m.forwardForm(msg)
}

func (m model) toggleFormFocus() (tea.Model, tea.Cmd) {
	if m.formFocus == 0 {
		m.formFocus = 1
		m.titleInput.Blur()
		return m, m.promptArea.Focus()
	}
	m.formFocus = 0
	m.promptArea.Blur()
	return m, m.titleInput.Focus()
}

func (m model) forwardForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.formFocus == 0 {
		m.titleInput, cmd = m.titleInput.Update(msg)
	} else {
		m.promptArea, cmd = m.promptArea.Update(msg)
	}
	return m, cmd
}

func (m model) saveForm() (tea.Model, tea.Cmd) {
	title := strings.TrimSpace(m.titleInput.Value())
	prompt := strings.TrimSpace(m.promptArea.Value())
	if prompt == "" {
		m.formErr = "The prompt can't be empty."
		return m, nil
	}
	if title == "" {
		title = firstLine(prompt, 60)
	}

	st := m.storeFor(m.formScope)
	if m.formMode == formAdd {
		err := st.add(Todo{ID: newID(), Title: title, Prompt: prompt, Created: time.Now()})
		if err != nil {
			m.formErr = "save failed: " + err.Error()
			return m, nil
		}
		m.setStatus("added to "+m.formScope.String()+" backlog", false)
	} else {
		err := st.update(Todo{ID: m.editID, Title: title, Prompt: prompt})
		if err != nil {
			m.formErr = "save failed: " + err.Error()
			return m, nil
		}
		m.setStatus("updated", false)
	}
	m.rebuildList()
	m.stage = stageList
	return m, nil
}

// --- Delete confirm -----------------------------------------------------------

func (m model) beginDelete() (tea.Model, tea.Cmd) {
	ref, ok := m.selectedRef()
	if !ok {
		return m, nil
	}
	td, _ := m.resolve(ref)
	m.pendingDelete = ref
	m.pendingTitle = firstNonEmpty(td.Title, firstLine(td.Prompt, 40))
	m.stage = stageConfirm
	return m, nil
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "y", "Y", "enter":
		if err := m.storeFor(m.pendingDelete.scope).delete(m.pendingDelete.id); err != nil {
			m.setStatus("delete failed: "+err.Error(), true)
		} else {
			m.setStatus("deleted", false)
		}
		m.rebuildList()
		m.stage = stageList
		return m, nil
	case "n", "N", "esc":
		m.stage = stageList
		return m, nil
	}
	return m, nil
}

// --- Target picker ------------------------------------------------------------

func (m model) beginDrop() (tea.Model, tea.Cmd) {
	ref, ok := m.selectedRef()
	if !ok {
		return m, nil
	}
	if m.dropping {
		m.setStatus("a drop is still in progress…", false)
		return m, nil
	}
	if m.client == nil {
		m.setStatus("herdr socket unavailable — can't drop into a session", true)
		return m, nil
	}
	m.dropTodo = ref
	m.targets, m.targetList = m.buildTargets()
	m.stage = stageTarget
	return m, textinput.Blink
}

// buildTargets assembles the drop destinations: a new Claude session, plus every
// agent pane herdr currently reports (Claude panes first), excluding our own.
func (m model) buildTargets() ([]dropTarget, fuzzyList) {
	wsLabel := firstNonEmpty(m.ctx.WorkspaceLabel, baseName(m.ctx.WorkDir), "the current project")
	targets := []dropTarget{{
		kind:  targetNewSession,
		label: "＋ New Claude Code session",
		desc:  "open a new tab in " + wsLabel + " and launch claude",
	}}

	if m.client != nil {
		if panes, err := m.client.paneList(); err == nil {
			own := os.Getenv("HERDR_PANE_ID")
			var agents []paneInfo
			for _, p := range panes {
				if p.Agent == "" || p.PaneID == own {
					continue
				}
				agents = append(agents, p)
			}
			sort.SliceStable(agents, func(i, j int) bool {
				return agents[i].Agent == "claude" && agents[j].Agent != "claude"
			})

			wsCache := map[string]string{}
			for _, p := range agents {
				loc, seen := wsCache[p.WorkspaceID]
				if !seen {
					if ws, err := m.client.workspaceGet(p.WorkspaceID); err == nil {
						loc = ws.Label
					}
					if loc == "" {
						loc = baseName(firstNonEmpty(p.ForegroundCwd, p.Cwd))
					}
					wsCache[p.WorkspaceID] = loc
				}
				name := firstNonEmpty(p.DisplayAgent, p.Agent)
				here := ""
				if p.WorkspaceID != "" && p.WorkspaceID == m.ctx.WorkspaceId {
					here = " (this project)"
				}
				targets = append(targets, dropTarget{
					kind:   targetExistingPane,
					paneID: p.PaneID,
					agent:  name,
					label:  fmt.Sprintf("%s · %s%s", name, firstNonEmpty(loc, "session"), here),
					desc:   firstNonEmpty(p.ForegroundCwd, p.Cwd),
				})
			}
		}
	}

	items := make([]listItem, len(targets))
	for i, t := range targets {
		items[i] = listItem{name: t.label, desc: t.desc, selectable: true, ref: i}
	}
	return targets, newFuzzyList("Filter targets…", items)
}

func (m model) updateTarget(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.stage = stageList
		return m, nil
	case "up", "ctrl+p":
		m.targetList.moveUp()
		return m, nil
	case "down", "ctrl+n":
		m.targetList.moveDown()
		return m, nil
	case "enter":
		return m.chooseTarget(dropPaste)
	case "ctrl+r":
		return m.chooseTarget(dropRun)
	}
	cmd := m.targetList.editQuery(msg)
	return m, cmd
}

func (m model) chooseTarget(mode dropMode) (tea.Model, tea.Cmd) {
	if m.dropping {
		return m, nil
	}
	idx := m.targetList.selectedIndex()
	if idx < 0 || idx >= len(m.targets) {
		return m, nil
	}
	td, ok := m.resolve(m.dropTodo)
	if !ok {
		m.setStatus("could not find that prompt", true)
		m.stage = stageList
		return m, nil
	}
	target := m.targets[idx]
	// Perform the drop without quitting: the pane persists, so we return to the
	// list and run the (potentially slow) drop off the UI thread, reporting back
	// via dropResultMsg. A drop into a new session focuses the freshly-created
	// Claude tab, leaving this manager alive in the background to reuse later.
	m.dropping = true
	m.stage = stageList
	m.setStatus("dropping into "+targetDesc(target)+"…", false)
	return m, m.performDropCmd(pendingAction{todo: td, target: target, mode: mode})
}

// performDropCmd runs the chosen drop in a goroutine (a tea.Cmd) so herdr's
// pane creation and claude launch — which can take several seconds — don't
// freeze the manager. It reports the destination and any error back as a
// dropResultMsg. The client and context are captured by value; performDrop only
// makes short-lived, independent socket calls, so this is safe to run alongside
// the still-rendering UI.
func (m model) performDropCmd(act pendingAction) tea.Cmd {
	client, ctx := m.client, m.ctx
	desc := targetDesc(act.target)
	return func() tea.Msg {
		return dropResultMsg{desc: desc, err: performDrop(client, ctx, act)}
	}
}

// targetDesc is a short, human label for a drop destination, used in the
// "dropping…" / "dropped →" status lines.
func targetDesc(t dropTarget) string {
	if t.kind == targetNewSession {
		return "new Claude Code session"
	}
	return firstNonEmpty(t.agent, "session")
}

// --- View ---------------------------------------------------------------------

func (m model) View() string {
	if m.quitting {
		return ""
	}
	switch m.stage {
	case stageForm:
		return m.viewForm()
	case stageConfirm:
		return m.viewConfirm()
	case stageTarget:
		return m.viewTarget()
	default:
		return m.viewList()
	}
}

func (m model) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📝 Herdr Todo — prompt backlog"))
	scopeNote := "global only"
	if m.project.available() {
		scopeNote = firstNonEmpty(m.ctx.WorkspaceLabel, baseName(m.ctx.WorkDir), "project") + " + global"
	}
	b.WriteString("  ")
	b.WriteString(descStyle.Render(scopeNote))
	b.WriteString("\n\n")

	b.WriteString(m.list.view("No prompts yet — press ctrl+a to add one."))

	if m.status != "" {
		b.WriteString("\n")
		st := okStyle
		if m.statusErr {
			st = errStyle
		}
		b.WriteString(st.Render("• " + m.status))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(footerStyle.Render("enter drop · ctrl+a add · ctrl+e edit · ctrl+t done · ctrl+x delete · esc quit"))
	return b.String()
}

func (m model) viewForm() string {
	var b strings.Builder
	heading := "New prompt — " + m.formScope.String() + " backlog"
	if m.formMode == formEdit {
		heading = "Edit prompt"
	}
	b.WriteString(titleStyle.Render(heading))
	b.WriteString("\n\n")

	b.WriteString(promptStyle.Render("Title"))
	b.WriteString("\n")
	b.WriteString(m.titleInput.View())
	b.WriteString("\n\n")

	b.WriteString(promptStyle.Render("Prompt"))
	b.WriteString("\n")
	b.WriteString(m.promptArea.View())
	b.WriteString("\n")

	if m.formErr != "" {
		b.WriteString(errStyle.Render(m.formErr))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	help := "tab switch field · ctrl+s save · esc cancel"
	if m.formMode == formAdd && m.project.available() {
		help = "tab switch field · ctrl+s save · ctrl+g toggle scope · esc cancel"
	}
	b.WriteString(footerStyle.Render(help))
	return b.String()
}

func (m model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Delete prompt?"))
	b.WriteString("\n\n")
	b.WriteString(nameStyle.Render("  " + truncate(m.pendingTitle, 70)))
	b.WriteString("\n\n")
	b.WriteString(footerStyle.Render("y delete · n / esc cancel"))
	return b.String()
}

func (m model) viewTarget() string {
	var b strings.Builder
	td, _ := m.resolve(m.dropTodo)
	title := firstNonEmpty(td.Title, firstLine(td.Prompt, 50))
	b.WriteString(titleStyle.Render("Drop into…"))
	b.WriteString("  ")
	b.WriteString(descStyle.Render(truncate(title, 60)))
	b.WriteString("\n\n")
	b.WriteString(m.targetList.view("no agent sessions detected — pick New Claude Code session"))
	b.WriteString("\n")
	b.WriteString(footerStyle.Render("enter paste (don't submit) · ctrl+r drop & run · esc back"))
	return b.String()
}

// applySizes pushes the current window size into the inputs of the active stage
// so they wrap and scroll to fit. Only the live stage's components are touched:
// the form's textinput/textarea and the target picker are zero-value until their
// stage is entered (built in beginAdd/beginEdit/beginDrop), and calling a method
// like textarea.SetWidth on a zero-value model dereferences nil internal state.
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

// baseName returns the last path element of p, or "" for an empty/"." path.
func baseName(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	b := filepath.Base(p)
	if b == "." || b == string(filepath.Separator) {
		return ""
	}
	return b
}
