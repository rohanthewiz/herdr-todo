package main

import (
	"fmt"
	"os"
)

// version is the plugin binary's version, kept in sync with herdr-plugin.toml.
const version = "0.1.0"

// main is the plugin binary's entry point. herdr-todo is a herdr plugin: herdr
// registers it from herdr-plugin.toml and runs this binary with a subcommand per
// manifest entry point.
//
//   - "todo" is the action herdr runs from a keybinding / action menu. It runs
//     server-side and asks herdr to open the manager UI as a plugin pane.
//   - "todo-ui" is that UI; herdr runs it inside the pane it opens (the `todo-ui`
//     entrypoint), so end users never run it directly.
//
// The bare binary has no launcher of its own, so it just prints usage.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "todo":
			launchTodo()
			return
		case "todo-ui":
			runTodoUI()
			return
		case "version", "--version", "-v", "-V":
			fmt.Println("herdr-todo", version)
			return
		}
	}
	errExit("a herdr plugin; run its action through herdr (e.g. `herdr plugin action invoke rohanthewiz.herdr-todo.todo`) or `herdr-todo version`.")
}
