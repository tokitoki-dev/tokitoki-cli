//go:build !linux && !darwin && !windows

package machineid

// rawMachineID has no source of machine identity on this platform; ID()
// reports the empty string and the server records the ping without one.
func rawMachineID() (string, error) {
	return "", nil
}
