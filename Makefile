APP := tokitoki
PKG := ./cmd/tokitoki

# Release builds stamp a semantic version (`make cross VERSION=1.2.0`). The
# default "dev" marks a local build, which the CLI refuses to self-update.
VERSION ?= dev
VERSION_VAR := github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo.Version

# The directory, under $$HOME, that a build owns: its API key, event queue and
# locks. Binaries stamped with different names share none of it, so any number
# of them coexist on one machine.
#
# DATA_DIR is what a local build owns, and it never names the installed
# directory: `make` and `make build` produce a binary that cannot touch the
# state a user already has, whatever else goes wrong.
#
# Shipping is the one thing that must claim that directory, so the release
# targets stamp RELEASE_DATA_DIR instead — a value no local target reads and
# no command line supplies. Nothing here defaults to it, so no local build can
# reach production state by omission; releases get it by construction rather
# than by remembering an argument.
DATA_DIR_VAR := github.com/tokitoki-dev/tokitoki-cli/internal/config.DataDirName
DATA_DIR ?= .tokitoki-dev
override RELEASE_DATA_DIR := .tokitoki

# The server a build reports to, under the same rule as DATA_DIR: a local
# build gets the local development server and can never reach production by
# omission; only the release targets stamp the production address. It is a
# build parameter, not an environment variable — the binary reads nothing
# from the environment to decide this, so every front-end that launches it
# (the desktop app, the editor plugins, a service unit) reaches the same
# server whether or not it thought to pass one along. `tokitoki server-url`
# prints what a binary was built with.
SERVER_URL_VAR := github.com/tokitoki-dev/tokitoki-cli/internal/config.ServerURL
SERVER_URL ?= http://localhost:9093
override RELEASE_SERVER_URL := https://tokitoki.dev

STAMPS := -X $(VERSION_VAR)=$(VERSION) -X $(DATA_DIR_VAR)=$(DATA_DIR) -X $(SERVER_URL_VAR)=$(SERVER_URL)
RELEASE_STAMPS := -X $(VERSION_VAR)=$(VERSION) -X $(DATA_DIR_VAR)=$(RELEASE_DATA_DIR) -X $(SERVER_URL_VAR)=$(RELEASE_SERVER_URL)

LDFLAGS := -ldflags "$(STAMPS)"

# Release builds also strip symbol tables (-s -w), cutting the binary by about
# a third. Panic stack traces still print; only debugger symbols are lost, so
# the local `build` target keeps them.
RELEASE_LDFLAGS := -ldflags "-s -w $(RELEASE_STAMPS)"
RELEASE_FLAGS := -trimpath -buildvcs=false $(RELEASE_LDFLAGS)

# Provider data roots to scan. Override on the command line to point at
# fixtures, e.g. `make run PROVIDER_DIRS='claude=/tmp/claude codex=/tmp/codex'`.
PROVIDER_DIRS ?= claude=$(HOME)/.claude codex=$(HOME)/.codex copilot=$(HOME)/.copilot/otel gemini=$(HOME)/.gemini/tmp kimi=$(HOME)/.kimi qwen=$(HOME)/.qwen openclaw=$(HOME)/.openclaw openclaw=$(HOME)/.clawdbot openclaw=$(HOME)/.moltbot openclaw=$(HOME)/.moldbot pi=$(HOME)/.pi/agent/sessions amp=$(HOME)/.local/share/amp droid=$(HOME)/.factory/sessions kilo=$(HOME)/.local/share/kilo hermes=$(HOME)/.hermes codebuff=$(HOME)/.config/manicode codebuff=$(HOME)/.config/manicode-dev codebuff=$(HOME)/.config/manicode-staging opencode=$(HOME)/.local/share/opencode goose=$(HOME)/.local/share/goose/sessions/sessions.db goose=$(HOME)/.local/share/Block/goose/sessions/sessions.db

.DEFAULT_GOAL := run

.PHONY: run test race build build-installed data-dir server-url agent-binary linux-amd64 tidy cross

# `make` is the quickest local integration check: build the CLI then run its
# complete scan-and-upload operation against SERVER_URL (the local development
# server unless overridden). Pass PROVIDER_DIRS to choose which data
# directories to scan.
run: build
	./bin/$(APP) $(foreach dir,$(PROVIDER_DIRS),--provider-dir $(dir))

test:
	go test ./...

race:
	go test -race ./...

build:
	mkdir -p bin
	go build $(LDFLAGS) -o bin/$(APP) $(PKG)

# A local build that owns the installed binary's directory and reports to the
# installed binary's server, for reproducing a problem against real state.
# Deliberately a separate target you ask for by name: no ordinary build reads
# that state or reaches that server.
build-installed:
	mkdir -p bin
	go build -ldflags "$(RELEASE_STAMPS)" -o bin/$(APP) $(PKG)

# Print the data directory the built binary actually uses, so "which state am
# I looking at?" is answered by the binary rather than by reading this file.
data-dir: build
	./bin/$(APP) data-dir

# Likewise for the server: the binary says where it reports, so nobody has to
# infer it from build flags.
server-url: build
	./bin/$(APP) server-url

# One cross-compiled binary for a bundling front-end (the VS Code extension
# builds all six this way). GOOS, GOARCH and OUTPUT say what to produce;
# VERSION, DATA_DIR and SERVER_URL say what it is, and default here exactly as
# they do for any other local build — which is the reason this target exists rather than
# each front-end writing its own `go build` line and having to remember the
# stamps. Release VSIXes do not come through here at all: they bundle the
# published release binaries.
#
#   make agent-binary GOOS=linux GOARCH=arm64 OUTPUT=/path/tokitoki-linux-arm64
agent-binary:
	@test -n "$(OUTPUT)" || { echo "agent-binary needs OUTPUT=<path>" >&2; exit 2; }
	@test -n "$(GOOS)" || { echo "agent-binary needs GOOS=<os>" >&2; exit 2; }
	@test -n "$(GOARCH)" || { echo "agent-binary needs GOARCH=<arch>" >&2; exit 2; }
	mkdir -p $(dir $(OUTPUT))
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	  go build -trimpath -ldflags "-s -w $(STAMPS)" \
	  -o $(OUTPUT) $(PKG)

linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(APP)-linux-amd64 $(PKG)

# Cross-compile the agent for every target platform. Pure Go (CGO disabled),
# so a single host builds all of them; the native front-ends bundle the
# matching binary.
cross:
	rm -rf dist
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(APP)-darwin-amd64  $(PKG)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(APP)-darwin-arm64  $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(APP)-windows-amd64.exe $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(APP)-windows-arm64.exe $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(APP)-linux-amd64   $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(APP)-linux-arm64   $(PKG)

tidy:
	go mod tidy
