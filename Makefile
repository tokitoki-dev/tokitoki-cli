APP := tokitoki-agent
PKG := ./cmd/tracklm-agent

.PHONY: run test race build tidy

run:
	go run $(PKG)

test:
	go test ./...

race:
	go test -race ./...

build:
	mkdir -p bin
	go build -o bin/$(APP) $(PKG)

tidy:
	go mod tidy
