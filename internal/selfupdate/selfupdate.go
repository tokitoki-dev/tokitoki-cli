// Package selfupdate replaces the running tokitoki binary with the newest
// published release.
//
// This is the single implementation of CLI updating for the whole product:
// the macOS/Windows apps and the editor plugins never download the CLI — they
// seed the shared binary from their bundled copy and invoke `tokitoki update`,
// and everything after that happens here, once, in Go.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
)

// UpdateChannel is the release channel the CLI reads on the TokiToki server.
const UpdateChannel = "cli"

const (
	lockName    = "upgrade.lock"
	lockTimeout = 2 * time.Second
	// oldSuffix marks the previous binary on Windows, where a running
	// executable cannot be removed but can be renamed aside.
	oldSuffix = ".old"
)

// Result is what one upgrade attempt concluded.
type Result struct {
	Updated bool
	// Version is the version now installed at the executable path.
	Version string
}

// semverRE accepts what the server's update check accepts. Anything else —
// "dev" above all — identifies a local build, which must never update itself.
var semverRE = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+`)

// Upgrade brings the running executable up to date against the TokiToki
// server. baseURL is the server root (usageupload.BaseURL()); current is the
// running version.
//
// The swap is atomic: the new binary lands in a temp file next to the
// executable and is renamed over it, so a concurrent invocation sees either
// the old binary or the new one, never a torn file.
func Upgrade(ctx context.Context, logger *slog.Logger, baseURL, current string) (Result, error) {
	if !semverRE.MatchString(current) {
		logger.Debug("skipping self-update for local build", "version", current)
		return Result{Version: current}, nil
	}

	executable, err := os.Executable()
	if err != nil {
		return Result{}, fmt.Errorf("locate executable: %w", err)
	}
	dataDir, err := store.InitializeDataDir()
	if err != nil {
		return Result{}, err
	}
	return upgradeExecutable(ctx, logger, baseURL, current, executable, dataDir)
}

// upgradeExecutable is Upgrade with the environment made explicit, so tests
// can point it at a scratch binary and a scratch lock directory.
func upgradeExecutable(ctx context.Context, logger *slog.Logger, baseURL, current, executable, lockDir string) (Result, error) {
	// A binary inside an app bundle is covered by the bundle's code
	// signature; rewriting it corrupts the app. Only the shared standalone
	// copy may update itself.
	if strings.Contains(executable, ".app/Contents/") {
		return Result{}, fmt.Errorf("refusing to update %s: it is part of an app bundle", executable)
	}

	// The whole point of a self-update is that its output becomes the next
	// thing we execute. Fetching executable code over cleartext HTTP hands
	// that to whoever sits on the path — loopback is the only exception,
	// because it never leaves the machine.
	if err := requireTrustedTransport(baseURL); err != nil {
		return Result{}, err
	}

	lock, err := store.AcquireLock(lockDir, lockName, lockTimeout)
	if errors.Is(err, store.ErrLockBusy) {
		// Another front-end is already upgrading this same binary.
		logger.Debug("self-update already in progress elsewhere")
		return Result{Version: current}, nil
	}
	if err != nil {
		return Result{}, err
	}
	defer lock.Close()

	// A leftover from a previous Windows swap; harmless to try everywhere.
	_ = os.Remove(executable + oldSuffix)

	update, err := check(ctx, baseURL, current)
	if err != nil {
		return Result{}, err
	}
	if update == nil {
		return Result{Version: current}, nil
	}

	tmp, err := download(ctx, baseURL, update, filepath.Dir(executable))
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(tmp)

	// The downloaded binary must run and must be the version the server
	// offered. This catches corrupt downloads, and it makes an update loop
	// impossible: a binary that would still report the old version is never
	// installed.
	if err := verify(ctx, tmp, update.Version); err != nil {
		return Result{}, err
	}

	if err := swap(tmp, executable); err != nil {
		return Result{}, err
	}
	logger.Info("tokitoki updated", "from", current, "to", update.Version)
	return Result{Updated: true, Version: update.Version}, nil
}

// updateInfo is the server's answer when an update is available.
type updateInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	// SHA256 is the asset's hex digest. Present whenever GitHub published
	// one; the download is rejected if it does not match.
	SHA256 string `json:"sha256"`
}

// requireTrustedTransport rejects base URLs that would fetch a binary over a
// connection an on-path attacker can rewrite.
func requireTrustedTransport(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("self-update server URL %q: %w", baseURL, err)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to self-update over %q: a non-loopback update server must use https", baseURL)
}

func check(ctx context.Context, baseURL, current string) (*updateInfo, error) {
	url := fmt.Sprintf(
		"%s/api/updates/check?channel=%s&platform=%s&arch=%s&version=%s",
		baseURL, UpdateChannel, platform(), runtime.GOARCH, strings.TrimPrefix(current, "v"),
	)
	resp, err := httpGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("check for update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check for update: server returned %s", resp.Status)
	}

	var update updateInfo
	if err := json.NewDecoder(resp.Body).Decode(&update); err != nil {
		return nil, fmt.Errorf("check for update: %w", err)
	}
	if !update.Available {
		return nil, nil
	}
	if update.URL == "" {
		return nil, fmt.Errorf("check for update: update %s has no download URL", update.Version)
	}
	return &update, nil
}

// download streams the new binary into a temp file inside dir — the
// executable's own directory, so the final rename never crosses filesystems.
func download(ctx context.Context, baseURL string, update *updateInfo, dir string) (string, error) {
	resp, err := httpGet(ctx, baseURL+update.URL)
	if err != nil {
		return "", fmt.Errorf("download update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download update: server returned %s", resp.Status)
	}

	file, err := os.CreateTemp(dir, ".tokitoki-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("download update: %w", err)
	}
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, digest), resp.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil && update.Size > 0 && written != update.Size {
		err = fmt.Errorf("got %d bytes, expected %d", written, update.Size)
	}
	// The digest is the integrity check: it ties the bytes on disk to the
	// bytes the release was published with, before anything executes them.
	// Old releases without a published digest fall back to the size check.
	if err == nil && update.SHA256 != "" {
		got := hex.EncodeToString(digest.Sum(nil))
		want := strings.ToLower(strings.TrimPrefix(update.SHA256, "sha256:"))
		if got != want {
			err = fmt.Errorf("sha256 mismatch: downloaded %s, server published %s", got, want)
		}
	}
	if err == nil {
		err = os.Chmod(file.Name(), 0o755)
	}
	if err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("download update: %w", err)
	}
	return file.Name(), nil
}

func verify(ctx context.Context, binary, wantVersion string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return fmt.Errorf("verify update: downloaded binary failed to run: %w", err)
	}
	got := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	want := strings.TrimPrefix(wantVersion, "v")
	if got != want {
		return fmt.Errorf("verify update: binary reports version %q, server offered %q", got, want)
	}
	return nil
}

func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	return http.DefaultClient.Do(req)
}

// platform maps GOOS onto the server's platform vocabulary.
func platform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}
