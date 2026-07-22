//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const (
	systemdServiceUnit = "tokitoki.service"
	systemdTimerUnit   = "tokitoki.timer"
)

// platformService manages the sync as a systemd oneshot service driven by a
// timer, not a resident daemon: each firing runs one `tokitoki --check-update`
// and exits, so there is no idle process and self-update needs no restart
// dance. Root installs system units under /etc/systemd/system, which run
// without a login session and need nothing else enabled. A plain user gets
// user units, which only fire while the user has a session unless lingering
// is enabled — install attempts `loginctl enable-linger` and says so when it
// cannot.
func platformService(action string, flags workerFlags, userService bool) int {
	switch action {
	case "install", "uninstall", "start", "stop", "restart", "status":
	default:
		fmt.Fprintf(os.Stderr, "unknown service action %q\n", action)
		return 2
	}

	system := os.Geteuid() == 0
	if !system && !userService {
		fmt.Fprintln(os.Stderr, "installing a system service requires root; rerun with sudo")
		return 2
	}

	logger := defaultLogger()
	unitDir, err := systemdUnitDir(system)
	if err != nil {
		return fail(logger, err)
	}

	switch action {
	case "install":
		err = installSystemdUnits(flags, system, unitDir)
	case "uninstall":
		err = uninstallSystemdUnits(system, unitDir)
	case "start":
		err = systemctl(system, "start", systemdTimerUnit)
	case "stop":
		err = systemctl(system, "stop", systemdTimerUnit)
	case "restart":
		err = systemctl(system, "restart", systemdTimerUnit)
	case "status":
		fmt.Fprintln(os.Stdout, systemdTimerStatus(system))
	}
	if err != nil {
		return fail(logger, err)
	}
	return 0
}

func installSystemdUnits(flags workerFlags, system bool, unitDir string) error {
	serviceText, timerText, err := systemdUnitTexts(flags, system)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(unitDir, systemdServiceUnit), []byte(serviceText), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(unitDir, systemdTimerUnit), []byte(timerText), 0o644); err != nil {
		return err
	}
	if err := systemctl(system, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(system, "enable", "--now", systemdTimerUnit); err != nil {
		return err
	}

	if !system {
		// User units only fire while the user has an open session. Lingering
		// keeps the user manager (and this timer) alive after logout.
		current, err := user.Current()
		if err == nil {
			if err := exec.Command("loginctl", "enable-linger", current.Username).Run(); err != nil {
				fmt.Fprintf(os.Stderr,
					"warning: could not enable lingering; the timer stops when you log out.\n"+
						"Run `sudo loginctl enable-linger %s` once, or reinstall with sudo for a system service.\n",
					current.Username)
			}
		}
	}
	return nil
}

func uninstallSystemdUnits(system bool, unitDir string) error {
	// Best effort: a half-installed service must still uninstall cleanly.
	_ = systemctl(system, "disable", "--now", systemdTimerUnit)
	for _, unit := range []string{systemdTimerUnit, systemdServiceUnit} {
		if err := os.Remove(filepath.Join(unitDir, unit)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return systemctl(system, "daemon-reload")
}

func systemdUnitTexts(flags workerFlags, system bool) (string, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", "", err
	}

	execStart := []string{systemdQuote(executable), "--check-update"}
	if flags.explicitDirs {
		for _, value := range providerDirArgs(flags.providerDirs) {
			execStart = append(execStart, "--provider-dir", systemdQuote(value))
		}
	}

	var service strings.Builder
	service.WriteString("[Unit]\n")
	service.WriteString("Description=Sync local AI usage to TokiToki\n")
	service.WriteString("Wants=network-online.target\n")
	service.WriteString("After=network-online.target\n\n")
	service.WriteString("[Service]\n")
	service.WriteString("Type=oneshot\n")
	if system {
		service.WriteString("User=" + systemdRunAsUser() + "\n")
	}
	if baseURL := os.Getenv("TOKITOKI_BASE_URL"); baseURL != "" {
		service.WriteString("Environment=" + systemdQuote("TOKITOKI_BASE_URL="+baseURL) + "\n")
	}
	service.WriteString("ExecStart=" + strings.Join(execStart, " ") + "\n")

	seconds := int(flags.interval.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	timer := fmt.Sprintf(`[Unit]
Description=Run the TokiToki usage sync on an interval

[Timer]
OnBootSec=2min
OnUnitActiveSec=%dsec
RandomizedDelaySec=1min
Persistent=true

[Install]
WantedBy=timers.target
`, seconds)

	return service.String(), timer, nil
}

// systemdRunAsUser is who a system unit syncs as: the sudo caller when
// installed via sudo, otherwise the current (root) user. The sync must run as
// the user whose home holds the API key and provider data, never blindly as
// root.
func systemdRunAsUser() string {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		return sudoUser
	}
	if current, err := user.Current(); err == nil {
		return current.Username
	}
	return "root"
}

func systemdUnitDir(system bool) (string, error) {
	if system {
		return "/etc/systemd/system", nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "systemd", "user"), nil
}

func systemctl(system bool, args ...string) error {
	if !system {
		args = append([]string{"--user"}, args...)
	}
	command := exec.Command("systemctl", args...)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func systemdTimerStatus(system bool) string {
	states := make([]string, 0, 2)
	for _, query := range []string{"is-enabled", "is-active"} {
		args := []string{query, systemdTimerUnit}
		if !system {
			args = append([]string{"--user"}, args...)
		}
		// is-enabled/is-active exit non-zero for disabled/inactive but still
		// print the state, which is exactly what status should show.
		output, _ := exec.Command("systemctl", args...).Output()
		state := strings.TrimSpace(string(output))
		if state == "" {
			state = "unknown"
		}
		states = append(states, state)
	}
	return strings.Join(states, ", ")
}

// systemdQuote wraps a value in systemd's double-quote syntax so paths and
// arguments containing spaces survive ExecStart parsing.
func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
