#!/bin/sh
#
# Build herdr-todo for `herdr plugin install` / `herdr plugin link`. herdr runs
# this as the manifest's [[build]] step, in the plugin root, with no plugin
# context. The result is ./bin/herdr-todo, which the manifest's action and pane
# invoke.

set -eu

mkdir -p bin

if ! command -v go >/dev/null 2>&1; then
	echo "herdr-todo: Go toolchain not found on PATH; install Go 1.26+ to build." >&2
	exit 1
fi

echo "herdr-todo: building from source (go build)…" >&2
exec go build -o bin/herdr-todo .
