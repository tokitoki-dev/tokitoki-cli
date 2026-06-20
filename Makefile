APP := tokitoki
PKG := ./cmd/tokitoki

# Data directories to scan. Override on the command line to point at fixtures,
# e.g. `make run CLAUDE_DIR=/tmp/claude CODEX_DIR=`. An empty value skips that
# provider; there is no default location inside the CLI itself.
CLAUDE_DIR ?= $(HOME)/.claude
CODEX_DIR  ?= $(HOME)/.codex

.DEFAULT_GOAL := run

.PHONY: run test race build tidy cross

# `make` is the quickest local integration check: build the CLI then run its
# complete scan-and-upload operation against http://localhost:9093. Pass
# CLAUDE_DIR / CODEX_DIR to choose which data directories to scan.
run: build
	./bin/$(APP) $(if $(CLAUDE_DIR),--claude-dir $(CLAUDE_DIR)) $(if $(CODEX_DIR),--codex-dir $(CODEX_DIR))

test:
	go test ./...

race:
	go test -race ./...

build:
	mkdir -p bin
	go build -o bin/$(APP) $(PKG)

# Cross-compile the agent for every target platform. Pure Go (CGO disabled),
# so a single host builds all of them; the native front-ends bundle the
# matching binary.
cross:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -o dist/$(APP)-darwin-amd64  $(PKG)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o dist/$(APP)-darwin-arm64  $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/$(APP)-windows-amd64.exe $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o dist/$(APP)-linux-amd64   $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o dist/$(APP)-linux-arm64   $(PKG)

tidy:
	go mod tidy
