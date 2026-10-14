package git

import (
	"os"
	"path/filepath"
	"strings"
)

// Remote is the address of the repository's remote — origin, or when there
// is none the first remote its config names — as git has it, so the server
// can link to the repository. Two things never come back: credentials in the
// address (https://user:token@host/…, how CI and token clones authenticate),
// and an address that is a path on this machine, which has no page to link
// to and would put this machine's layout on the server.
func (r Repo) Remote() string {
	if r.commonDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(r.commonDir, "config"))
	if err != nil {
		return ""
	}
	var first, origin, remote string
	inRemote := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			remote, inRemote = remoteName(line)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !inRemote || !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		value = configValue(value)
		if first == "" {
			first = value
		}
		if remote == "origin" && origin == "" {
			origin = value
		}
	}
	if origin != "" {
		return publicAddress(origin)
	}
	return publicAddress(first)
}

// remoteName reads the remote a config section header opens: origin for
// [remote "origin"]. Section names ignore case; remote names do not.
func remoteName(header string) (string, bool) {
	inner, _, _ := strings.Cut(strings.TrimPrefix(header, "["), "]")
	section, name, ok := strings.Cut(strings.TrimSpace(inner), " ")
	if !ok || !strings.EqualFold(section, "remote") {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(name), `"`), true
}

// configValue reads a config value: quotes removed, a trailing comment cut.
func configValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if quoted, ok := strings.CutPrefix(raw, `"`); ok {
		value, _, _ := strings.Cut(quoted, `"`)
		return value
	}
	if comment := strings.IndexAny(raw, "#;"); comment >= 0 {
		raw = raw[:comment]
	}
	return strings.TrimSpace(raw)
}

// publicAddress is a remote address fit to leave this machine: as written,
// less any credentials, or "" for a path on this machine.
func publicAddress(raw string) string {
	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		end := strings.IndexByte(rest, '/')
		if end < 0 {
			end = len(rest)
		}
		host := rest[strings.LastIndex(rest[:end], "@")+1 : end]
		if host == "" || strings.EqualFold(scheme, "file") {
			return ""
		}
		return scheme + "://" + host + rest[end:]
	}
	// The scp form, [user@]host:path, carries no password. Without a colon,
	// or with a slash before it, the address is a path; a one-letter host is
	// a Windows drive (C:\repos\app).
	before, _, ok := strings.Cut(raw, ":")
	if !ok || strings.ContainsAny(before, `/\`) {
		return ""
	}
	if host := before[strings.LastIndex(before, "@")+1:]; len(host) < 2 {
		return ""
	}
	return raw
}
