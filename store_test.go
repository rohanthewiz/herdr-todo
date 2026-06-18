package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tempStore returns a project store backed by a fresh file under t.TempDir(), so
// each test gets isolated, auto-cleaned persistence.
func tempStore(t *testing.T) *store {
	t.Helper()
	return &store{scope: scopeProject, path: filepath.Join(t.TempDir(), "todos.json")}
}

func TestStoreAvailable(t *testing.T) {
	if (&store{path: ""}).available() {
		t.Error("a store with no path should be unavailable")
	}
	if !(&store{path: "/x/todos.json"}).available() {
		t.Error("a store with a path should be available")
	}
}

// TestUnavailableStoreTouchesNoDisk pins the documented contract that an
// unavailable store (an empty path, e.g. launched outside a project) loads and
// saves to nothing rather than erroring or writing a stray file.
func TestUnavailableStoreTouchesNoDisk(t *testing.T) {
	dir := t.TempDir()
	s := &store{scope: scopeProject, path: ""}

	if err := s.load(); err != nil {
		t.Fatalf("load on unavailable store = %v, want nil", err)
	}
	if s.todos != nil {
		t.Errorf("load left todos = %v, want nil", s.todos)
	}
	if err := s.save(); err != nil {
		t.Fatalf("save on unavailable store = %v, want nil", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("save on unavailable store wrote %d entries, want 0", len(entries))
	}
}

// TestSaveLoadRoundTrip writes todos through one store and reads them back
// through a second store at the same path, proving on-disk persistence survives.
func TestSaveLoadRoundTrip(t *testing.T) {
	s := tempStore(t)
	created := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	want := []Todo{
		{ID: "a1", Title: "first", Prompt: "do the first thing", Created: created},
		{ID: "b2", Title: "", Prompt: "no title here", Done: true, Created: created},
	}
	s.todos = want
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded := &store{scope: scopeProject, path: s.path}
	if err := reloaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(reloaded.todos) != len(want) {
		t.Fatalf("loaded %d todos, want %d", len(reloaded.todos), len(want))
	}
	for i, td := range reloaded.todos {
		if td.ID != want[i].ID || td.Title != want[i].Title || td.Prompt != want[i].Prompt || td.Done != want[i].Done {
			t.Errorf("todo[%d] = %+v, want %+v", i, td, want[i])
		}
		if !td.Created.Equal(want[i].Created) {
			t.Errorf("todo[%d].Created = %v, want %v", i, td.Created, want[i].Created)
		}
	}
}

func TestLoadMissingFileIsEmptyNotError(t *testing.T) {
	// A first run has no file yet — that must read as an empty backlog.
	s := &store{scope: scopeGlobal, path: filepath.Join(t.TempDir(), "does-not-exist.json")}
	if err := s.load(); err != nil {
		t.Fatalf("load of missing file = %v, want nil", err)
	}
	if len(s.todos) != 0 {
		t.Errorf("load of missing file yielded %d todos, want 0", len(s.todos))
	}
}

func TestLoadEmptyFileIsEmptyNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todos.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := &store{scope: scopeGlobal, path: path}
	if err := s.load(); err != nil {
		t.Fatalf("load of empty file = %v, want nil", err)
	}
	if len(s.todos) != 0 {
		t.Errorf("load of empty file yielded %d todos, want 0", len(s.todos))
	}
}

func TestStoreCRUD(t *testing.T) {
	s := tempStore(t)

	if err := s.add(Todo{ID: "1", Title: "one", Prompt: "p1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.add(Todo{ID: "2", Title: "two", Prompt: "p2"}); err != nil {
		t.Fatal(err)
	}

	t.Run("find", func(t *testing.T) {
		got, ok := s.find("2")
		if !ok || got.Title != "two" {
			t.Errorf("find(2) = %+v, %v; want title two, true", got, ok)
		}
		if _, ok := s.find("nope"); ok {
			t.Error("find(nope) reported found for an unknown id")
		}
		// find returns a copy — mutating it must not touch the store.
		got.Title = "mutated"
		if again, _ := s.find("2"); again.Title != "two" {
			t.Errorf("find returned a live reference: store title became %q", again.Title)
		}
	})

	t.Run("update edits title and prompt only", func(t *testing.T) {
		if err := s.update(Todo{ID: "1", Title: "one-edited", Prompt: "p1-edited"}); err != nil {
			t.Fatal(err)
		}
		got, _ := s.find("1")
		if got.Title != "one-edited" || got.Prompt != "p1-edited" {
			t.Errorf("after update find(1) = %+v, want edited title/prompt", got)
		}
		// Updating an unknown id is a silent no-op, not an error.
		if err := s.update(Todo{ID: "ghost"}); err != nil {
			t.Errorf("update of unknown id = %v, want nil", err)
		}
	})

	t.Run("toggle flips done", func(t *testing.T) {
		if err := s.toggle("2"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.find("2"); !got.Done {
			t.Error("toggle(2) did not mark done")
		}
		if err := s.toggle("2"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.find("2"); got.Done {
			t.Error("second toggle(2) did not reopen")
		}
	})

	t.Run("delete removes and persists", func(t *testing.T) {
		if err := s.delete("1"); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.find("1"); ok {
			t.Error("delete(1) left the todo in memory")
		}
		// Reload from disk to confirm the deletion was saved, not just in-memory.
		reloaded := &store{scope: s.scope, path: s.path}
		if err := reloaded.load(); err != nil {
			t.Fatal(err)
		}
		if len(reloaded.todos) != 1 || reloaded.todos[0].ID != "2" {
			t.Errorf("after delete, disk has %+v, want only id 2", reloaded.todos)
		}
	})
}

func TestProjectTodosPath(t *testing.T) {
	if got := projectTodosPath(""); got != "" {
		t.Errorf("projectTodosPath(\"\") = %q, want empty (no project scope)", got)
	}
	want := filepath.Join("/repo", projectConfigDirName, "todos.json")
	if got := projectTodosPath("/repo"); got != want {
		t.Errorf("projectTodosPath(/repo) = %q, want %q", got, want)
	}
}

// TestConfigBaseDir exercises the documented precedence: the herdr-managed
// per-plugin dir wins, then XDG_CONFIG_HOME/herdr-todo, then ~/.config. These use
// t.Setenv and so must not run in parallel.
func TestConfigBaseDir(t *testing.T) {
	t.Run("HERDR_PLUGIN_CONFIG_DIR wins", func(t *testing.T) {
		t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "/herdr/plugin/cfg")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := configBaseDir()
		if err != nil {
			t.Fatal(err)
		}
		if got != "/herdr/plugin/cfg" {
			t.Errorf("configBaseDir = %q, want the herdr plugin dir", got)
		}
	})

	t.Run("falls back to XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := configBaseDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("/xdg", "herdr-todo"); got != want {
			t.Errorf("configBaseDir = %q, want %q", got, want)
		}
	})

	t.Run("falls back to home .config", func(t *testing.T) {
		t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir available: %v", err)
		}
		got, err := configBaseDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, ".config", "herdr-todo"); got != want {
			t.Errorf("configBaseDir = %q, want %q", got, want)
		}
	})
}

// TestLoadStores checks that a launch with a project workdir produces an
// available, loaded project store plus the global store, while an empty workdir
// leaves the project store unavailable (global-only mode).
func TestLoadStores(t *testing.T) {
	// Point the global backlog at an isolated dir so the test never touches the
	// real user config.
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())

	t.Run("with a project workdir", func(t *testing.T) {
		work := t.TempDir()
		project, global, err := loadStores(RunContext{WorkDir: work})
		if err != nil {
			t.Fatal(err)
		}
		if !project.available() {
			t.Error("project store should be available when WorkDir is set")
		}
		if project.path != projectTodosPath(work) {
			t.Errorf("project path = %q, want %q", project.path, projectTodosPath(work))
		}
		if !global.available() {
			t.Error("global store should always be available")
		}
	})

	t.Run("without a workdir is global-only", func(t *testing.T) {
		project, global, err := loadStores(RunContext{WorkDir: ""})
		if err != nil {
			t.Fatal(err)
		}
		if project.available() {
			t.Error("project store should be unavailable with no WorkDir")
		}
		if !global.available() {
			t.Error("global store should still be available")
		}
	})
}
