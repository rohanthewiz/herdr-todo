package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Todo is one saved prompt of future work.
type Todo struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Prompt  string    `json:"prompt"`
	Done    bool      `json:"done"`
	Created time.Time `json:"created"`
}

// scope marks where a todo lives: with the project (committed in the repo) or in
// the user's global backlog.
type scope int

const (
	// scopeProject is a todo stored in <workDir>/.herdr-todo/todos.json — it
	// travels with the repo and is only visible when you launch from that project.
	scopeProject scope = iota
	// scopeGlobal is a todo stored in the herdr-managed per-plugin config dir,
	// visible from anywhere.
	scopeGlobal
)

func (s scope) String() string {
	if s == scopeProject {
		return "Project"
	}
	return "Global"
}

// projectConfigDirName is the directory a repo gets for its herdr-todo config.
const projectConfigDirName = ".herdr-todo"

// configBaseDir returns the root config directory for herdr-todo's global
// backlog. When herdr runs us as a plugin it sets HERDR_PLUGIN_CONFIG_DIR (the
// herdr-managed, per-plugin dir that survives upgrades); we prefer it. Outside
// herdr we fall back to $XDG_CONFIG_HOME/herdr-todo, then ~/.config/herdr-todo.
func configBaseDir() (string, error) {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "herdr-todo"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "herdr-todo"), nil
}

// globalTodosPath returns the path to the global backlog file.
func globalTodosPath() (string, error) {
	base, err := configBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "todos.json"), nil
}

// projectTodosPath returns the project backlog file within workDir, or "" when
// workDir is unknown (no project scope is available in that case).
func projectTodosPath(workDir string) string {
	if workDir == "" {
		return ""
	}
	return filepath.Join(workDir, projectConfigDirName, "todos.json")
}

// store is a file-backed list of todos for one scope. A store with an empty path
// is "unavailable" (e.g. the project store when launched outside a project): it
// loads and saves to nothing and holds no todos.
type store struct {
	scope scope
	path  string
	todos []Todo
}

// available reports whether this scope has a backing file (a project store has
// none when there is no project directory).
func (s *store) available() bool { return s.path != "" }

// load reads the store's file into s.todos. A missing file is not an error — it
// just yields an empty list, which is the natural first-run state.
func (s *store) load() error {
	s.todos = nil
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &s.todos)
}

// save writes s.todos back to its file, creating the parent directory as needed.
// It is a no-op for an unavailable store.
func (s *store) save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.todos, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o644)
}

// add appends a new todo and persists.
func (s *store) add(t Todo) error {
	s.todos = append(s.todos, t)
	return s.save()
}

// update replaces the todo with the same ID (title/prompt) and persists.
func (s *store) update(t Todo) error {
	for i := range s.todos {
		if s.todos[i].ID == t.ID {
			s.todos[i].Title = t.Title
			s.todos[i].Prompt = t.Prompt
			return s.save()
		}
	}
	return nil
}

// delete removes the todo with id and persists.
func (s *store) delete(id string) error {
	out := s.todos[:0]
	for _, t := range s.todos {
		if t.ID != id {
			out = append(out, t)
		}
	}
	s.todos = out
	return s.save()
}

// toggle flips the done flag of the todo with id and persists.
func (s *store) toggle(id string) error {
	for i := range s.todos {
		if s.todos[i].ID == id {
			s.todos[i].Done = !s.todos[i].Done
			return s.save()
		}
	}
	return nil
}

// setDone sets the done flag of the todo with id to done and persists. Unlike
// toggle it is idempotent (a no-op when already in that state) — used to
// auto-complete a todo after a "run" drop without risk of reopening one that
// was already done.
func (s *store) setDone(id string, done bool) error {
	for i := range s.todos {
		if s.todos[i].ID == id {
			if s.todos[i].Done == done {
				return nil
			}
			s.todos[i].Done = done
			return s.save()
		}
	}
	return nil
}

// find returns a copy of the todo with id, and whether it was found.
func (s *store) find(id string) (Todo, bool) {
	for _, t := range s.todos {
		if t.ID == id {
			return t, true
		}
	}
	return Todo{}, false
}

// loadStores builds and loads the project and global stores for a launch
// context. The project store is keyed off ctx.WorkDir; when that is empty (no
// project) the project store is unavailable and only the global backlog shows.
func loadStores(ctx RunContext) (project *store, global *store, err error) {
	project = &store{scope: scopeProject, path: projectTodosPath(ctx.WorkDir)}
	if err = project.load(); err != nil {
		return nil, nil, err
	}

	gp, err := globalTodosPath()
	if err != nil {
		return nil, nil, err
	}
	global = &store{scope: scopeGlobal, path: gp}
	if err = global.load(); err != nil {
		return nil, nil, err
	}
	return project, global, nil
}
