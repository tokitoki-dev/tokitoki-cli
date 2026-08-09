//go:build darwin

package machineid

import (
	"errors"
	"os/exec"
	"strings"
)

// rawMachineID reads IOPlatformUUID, the hardware UUID macOS assigns once per
// machine. ioreg ships with the base system.
func rawMachineID() (string, error) {
	out, err := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		// The line reads: "IOPlatformUUID" = "XXXXXXXX-...". Splitting on
		// quotes puts the UUID in the fourth field.
		parts := strings.Split(line, "\"")
		if len(parts) >= 4 && strings.TrimSpace(parts[3]) != "" {
			return parts[3], nil
		}
	}
	return "", errors.New("IOPlatformUUID not found")
}
