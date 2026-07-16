APP := tokitoki
PKG := ./cmd/tokitoki

# Release builds stamp a semantic version (`make cross VERSION=1.2.0`). The
# default "dev" marks a local build, which the CLI refuses to self-update.
VERSION ?= dev
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# Release builds also strip symbol tables (-s -w), cutting the binary by about
# a third. Panic stack traces still print; only debugger symbols are lost, so
# the local `build` target keeps them.
RELEASE_LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

# Provider data roots to scan. Override on the command line to point at
# fixtures, e.g. `make run PROVIDER_DIRS='claude=/tmp/claude codex=/tmp/codex'`.
PROVIDER_DIRS ?= claude=$(HOME)/.claude codex=$(HOME)/.codex copilot=$(HOME)/.copilot/otel gemini=$(HOME)/.gemini/tmp kimi=$(HOME)/.kimi qwen=$(HOME)/.qwen openclaw=$(HOME)/.openclaw openclaw=$(HOME)/.clawdbot openclaw=$(HOME)/.moltbot openclaw=$(HOME)/.moldbot pi=$(HOME)/.pi/agent/sessions amp=$(HOME)/.local/share/amp droid=$(HOME)/.factory/sessions kilo=$(HOME)/.local/share/kilo hermes=$(HOME)/.hermes codebuff=$(HOME)/.config/manicode codebuff=$(HOME)/.config/manicode-dev codebuff=$(HOME)/.config/manicode-staging opencode=$(HOME)/.local/share/opencode goose=$(HOME)/.local/share/goose/sessions/sessions.db goose=$(HOME)/.local/share/Block/goose/sessions/sessions.db

.DEFAULT_GOAL := run

.PHONY: run test race build linux-amd64 tidy cross

# `make` is the quickest local integration check: build the CLI then run its
# complete scan-and-upload operation against http://localhost:9093. Pass
# PROVIDER_DIRS to choose which data directories to scan.
run: build
	./bin/$(APP) $(foreach dir,$(PROVIDER_DIRS),--provider-dir $(dir))

test:
	go test ./...

race:
	go test -race ./...

build:
	mkdir -p bin
	go build $(LDFLAGS) -o bin/$(APP) $(PKG)

linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(RELEASE_LDFLAGS) -o dist/$(APP)-linux-amd64 $(PKG)

# Cross-compile the agent for every target platform. Pure Go (CGO disabled),
# so a single host builds all of them; the native front-ends bundle the
# matching binary.
cross:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build $(RELEASE_LDFLAGS) -o dist/$(APP)-darwin-amd64  $(PKG)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(RELEASE_LDFLAGS) -o dist/$(APP)-darwin-arm64  $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(RELEASE_LDFLAGS) -o dist/$(APP)-windows-amd64.exe $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(RELEASE_LDFLAGS) -o dist/$(APP)-linux-amd64   $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build $(RELEASE_LDFLAGS) -o dist/$(APP)-linux-arm64   $(PKG)

tidy:
	go mod tidy
