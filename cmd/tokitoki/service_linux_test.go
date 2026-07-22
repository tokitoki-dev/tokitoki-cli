//go:build linux

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/pkg/agentlib"
)

func TestSystemdUnitTextsDefaultDirsStayUnbaked(t *testing.T) {
	flags := workerFlags{
		providerDirs: agentlib.DefaultProviderDirs(),
		explicitDirs: false,
		interval:     5 * time.Minute,
	}
	service, timer, err := systemdUnitTexts(flags, false)
	if err != nil {
		t.Fatalf("systemdUnitTexts() error = %v", err)
	}
	if strings.Contains(service, "--provider-dir") {
		t.Fatalf("default provider dirs must not be baked into ExecStart:\n%s", service)
	}
	if !strings.Contains(service, "--check-update") {
		t.Fatalf("ExecStart must include --check-update:\n%s", service)
	}
	if strings.Contains(service, "User=") {
		t.Fatalf("user units must not set User=:\n%s", service)
	}
	if !strings.Contains(timer, "OnUnitActiveSec=300sec") {
		t.Fatalf("timer must encode the interval in seconds:\n%s", timer)
	}
	if !strings.Contains(timer, "Persistent=true") {
		t.Fatalf("timer must be persistent:\n%s", timer)
	}
}

func TestSystemdUnitTextsExplicitDirsAndSystemUser(t *testing.T) {
	t.Setenv("SUDO_USER", "deploy")
	flags := workerFlags{
		providerDirs: map[agentlib.Provider][]string{
			"claude": {"/srv/agent data/claude"},
		},
		explicitDirs: true,
		interval:     30 * time.Second,
	}
	service, timer, err := systemdUnitTexts(flags, true)
	if err != nil {
		t.Fatalf("systemdUnitTexts() error = %v", err)
	}
	if !strings.Contains(service, `--provider-dir "claude=/srv/agent data/claude"`) {
		t.Fatalf("explicit provider dirs must be baked and quoted:\n%s", service)
	}
	if !strings.Contains(service, "User=deploy\n") {
		t.Fatalf("system units must run as the sudo caller:\n%s", service)
	}
	if !strings.Contains(timer, "OnUnitActiveSec=30sec") {
		t.Fatalf("timer must encode the interval in seconds:\n%s", timer)
	}
}

func TestPlatformServiceRejectsUnknownActionBeforeTouchingSystemd(t *testing.T) {
	flags := workerFlags{
		providerDirs: agentlib.DefaultProviderDirs(),
		interval:     time.Minute,
	}
	if code := platformService("bogus", flags, true); code != 2 {
		t.Fatalf("platformService(bogus) = %d, want 2", code)
	}
}
