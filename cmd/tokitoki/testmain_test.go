package main

import (
	"os"
	"testing"
)

// TestMain fences the whole package off from the machine it runs on.
//
// An unstamped test binary is a development build (config.DataDirName,
// config.ServerURL), so the worst it could reach is ~/.tokitoki-dev and the
// local development server. That is still a developer's real key and queue,
// and a real server with a real database behind it — and these tests run
// whole commands, which write a run log and, after `set key`, ping the server
// unthrottled. So: a throwaway home, and no telemetry. Tests that need a home
// of their own keep setting one with t.Setenv; this is the floor beneath the
// ones that never thought about it.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "tokitoki-cmd-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Setenv("TOKITOKI_NO_TELEMETRY", "1")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
