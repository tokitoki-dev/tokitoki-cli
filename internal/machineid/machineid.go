// Package machineid derives a stable, anonymous identifier for this machine
// from the identity the operating system already maintains. Nothing is
// generated or persisted: the same machine always produces the same value,
// so a wiped ~/.tokitoki or a reinstall never counts as a second install.
package machineid

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// appID is the HMAC message. Keying the HMAC with the raw machine id and
// hashing this constant means the reported value is unique to Tokitoki: it
// cannot be matched against the raw id or against what other software derives
// from the same machine.
const appID = "tokitoki"

// ID returns the anonymous identifier for this machine, or "" when the OS
// provides no machine identity (some containers, exotic platforms). Callers
// report the empty string as-is; inventing an id here would just be a worse
// version of the file this package exists to avoid.
func ID() string {
	raw, err := rawMachineID()
	raw = strings.TrimSpace(raw)
	if err != nil || raw == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(raw))
	mac.Write([]byte(appID))
	return hex.EncodeToString(mac.Sum(nil))
}
