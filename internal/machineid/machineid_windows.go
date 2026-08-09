//go:build windows

package machineid

import (
	"golang.org/x/sys/windows/registry"
)

// rawMachineID reads MachineGuid, generated once at Windows installation.
// WOW64_64KEY makes a 32-bit build read the same 64-bit hive as everyone
// else instead of a redirected view with a different value.
func rawMachineID() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()
	id, _, err := key.GetStringValue("MachineGuid")
	return id, err
}
