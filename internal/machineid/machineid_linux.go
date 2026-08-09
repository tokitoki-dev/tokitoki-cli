//go:build linux

package machineid

import (
	"errors"
	"os"
	"strings"
)

// rawMachineID reads the systemd machine id. The dbus path is the same value
// mirrored at its historical location, still the only one present on some
// older or containerized systems.
func rawMachineID() (string, error) {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			return string(data), nil
		}
	}
	return "", errors.New("machine id not found")
}
