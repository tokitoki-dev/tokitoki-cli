APP := tokitoki
PKG := ./cmd/tokitoki

# Provider data roots to scan. Override on the command line to point at
# fixtures, e.g. `make run PROVIDER_DIRS='claude=/tmp/claude codex=/tmp/codex'`.
PROVIDER_DIRS ?= claude=$(HOME)/.claude codex=$(HOME)/.codex copilot=$(HOME)/.copilot/otel gemini=$(HOME)/.gemini/tmp kimi=$(HOME)/.kimi qwen=$(HOME)/.qwen openclaw=$(HOME)/.openclaw openclaw=$(HOME)/.clawdbot openclaw=$(HOME)/.moltbot openclaw=$(HOME)/.moldbot pi=$(HOME)/.pi/agent/sessions amp=$(HOME)/.local/share/amp

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
	go build -o bin/$(APP) $(PKG)

linux-amd64:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/$(APP)-linux-amd64 $(PKG)

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
