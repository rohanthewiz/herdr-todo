# herdr-todo developer Makefile.

BIN := bin/herdr-todo

.PHONY: build vet test tidy link unlink relink clean

build:
	sh scripts/build.sh

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy

# Register this checkout with the running herdr (builds via the [[build]] step).
link:
	herdr plugin link .

unlink:
	herdr plugin unlink rohanthewiz.herdr-todo

# Rebuild and re-register after code changes.
relink: build
	herdr plugin unlink rohanthewiz.herdr-todo 2>/dev/null || true
	herdr plugin link .

clean:
	rm -rf bin
