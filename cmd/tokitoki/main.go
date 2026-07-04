// Command tokitoki scans local Claude Code/Codex usage files and uploads the
// resulting events to the local TokiToki server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	daemonservice "github.com/kardianos/service"
	"github.com/labx/tokitoki-agent/pkg/agentlib"
)

const (
	defaultSyncInterval = 5 * time.Minute
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		usage()
		return 0
	}
	if len(args) > 0 && args[0] == "set" {
		return runSet(args[1:])
	}
	if len(args) > 0 && args[0] == "get" {
		return runGet(args[1:])
	}
	if len(args) > 0 && args[0] == "__service-run" {
		return runServiceWorker(args[1:])
	}
	if len(args) > 0 && args[0] == "service" {
		return runService(args[1:])
	}

	runFlags, ok := parseRunFlags(args)
	if !ok {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	syncCtx, cancel := context.WithTimeout(ctx, agentlib.DefaultUploadTimeout)
	defer cancel()
	if err := runSync(syncCtx, runFlags.providerDirs, os.Stdout); err != nil {
		return fail(defaultLogger(), err)
	}
	return 0
}

func parseRunFlags(args []string) (workerFlags, bool) {
	flags := flag.NewFlagSet("tokitoki", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerDirs := newProviderDirFlags(agentlib.DefaultProviderDirs())
	flags.Var(providerDirs, "provider-dir", "provider data directory to scan (provider=dir; repeatable)")
	if err := flags.Parse(args); err != nil {
		return workerFlags{}, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tokitoki does not use subcommands; run `tokitoki --help`")
		return workerFlags{}, false
	}
	dirs := providerDirs.ProviderDirs()
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to scan; pass --provider-dir provider=dir")
		return workerFlags{}, false
	}
	return workerFlags{providerDirs: dirs}, true
}

func runSync(ctx context.Context, providerDirs map[agentlib.Provider][]string, out io.Writer) error {
	client, err := agentlib.New(agentlib.Options{Logger: defaultLogger()})
	if err != nil {
		return err
	}
	if err := client.Sync(ctx, agentlib.SyncOptions{ProviderDirs: providerDirs}); err != nil {
		return err
	}
	return writeJSON(out, map[string]bool{"ok": true})
}

func runSet(args []string) int {
	if len(args) != 2 || args[0] != "key" {
		fmt.Fprintln(os.Stderr, "usage: tokitoki set key <API_KEY>")
		return 2
	}

	logger := defaultLogger()
	client, err := agentlib.New(agentlib.Options{Logger: logger})
	if err != nil {
		return fail(logger, err)
	}
	if err := client.SetAPIKey(args[1]); err != nil {
		return fail(logger, err)
	}
	if err := writeJSON(os.Stdout, map[string]bool{"ok": true}); err != nil {
		return fail(logger, err)
	}
	return 0
}

func runGet(args []string) int {
	if len(args) != 1 || args[0] != "key" {
		fmt.Fprintln(os.Stderr, "usage: tokitoki get key")
		return 2
	}

	logger := defaultLogger()
	client, err := agentlib.New(agentlib.Options{Logger: logger})
	if err != nil {
		return fail(logger, err)
	}
	apiKey, err := client.GetAPIKey()
	if err != nil {
		return fail(logger, err)
	}
	fmt.Fprintln(os.Stdout, apiKey)
	return 0
}

type workerFlags struct {
	providerDirs map[agentlib.Provider][]string
	interval     time.Duration
}

func runServiceWorker(args []string) int {
	flags, ok := parseWorkerFlags("tokitoki __service-run", args)
	if !ok {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWorkerLoop(ctx, flags)
}

func runWorkerLoop(ctx context.Context, flags workerFlags) int {
	logger := defaultLogger()
	ticker := time.NewTicker(flags.interval)
	defer ticker.Stop()

	for {
		syncCtx, cancel := context.WithTimeout(ctx, agentlib.DefaultUploadTimeout)
		if err := runSync(syncCtx, flags.providerDirs, os.Stdout); err != nil {
			logger.Error("tokitoki sync failed", "error", err)
		}
		cancel()

		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}

type serviceProgram struct {
	flags  workerFlags
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *serviceProgram) Start(_ daemonservice.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		_ = runWorkerLoop(ctx, p.flags)
	}()
	return nil
}

func (p *serviceProgram) Stop(_ daemonservice.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

func runService(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tokitoki service <install|uninstall|start|stop|restart|status> [options]")
		return 2
	}

	action := args[0]
	flags, userService, ok := parseServiceFlags(args[1:])
	if !ok {
		return 2
	}
	service, err := newService(flags, userService)
	if err != nil {
		return fail(defaultLogger(), err)
	}

	switch action {
	case "install":
		err = service.Install()
	case "uninstall":
		err = service.Uninstall()
	case "start":
		err = service.Start()
	case "stop":
		err = service.Stop()
	case "restart":
		err = service.Restart()
	case "status":
		var status daemonservice.Status
		status, err = service.Status()
		if err == nil {
			fmt.Fprintln(os.Stdout, serviceStatusString(status))
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service action %q\n", action)
		return 2
	}
	if err != nil {
		return fail(defaultLogger(), err)
	}
	return 0
}

func parseWorkerFlags(name string, args []string) (workerFlags, bool) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerDirs := newProviderDirFlags(agentlib.DefaultProviderDirs())
	flags.Var(providerDirs, "provider-dir", "provider data directory to scan (provider=dir; repeatable)")
	interval := flags.Duration("interval", defaultSyncInterval, "sync interval")
	if err := flags.Parse(args); err != nil {
		return workerFlags{}, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "%s does not accept positional arguments\n", name)
		return workerFlags{}, false
	}
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be greater than zero")
		return workerFlags{}, false
	}
	dirs := providerDirs.ProviderDirs()
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to scan; pass --provider-dir provider=dir")
		return workerFlags{}, false
	}
	return workerFlags{
		providerDirs: dirs,
		interval:     *interval,
	}, true
}

func parseServiceFlags(args []string) (workerFlags, bool, bool) {
	flags := flag.NewFlagSet("tokitoki service", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerDirs := newProviderDirFlags(agentlib.DefaultProviderDirs())
	flags.Var(providerDirs, "provider-dir", "provider data directory to scan (provider=dir; repeatable)")
	interval := flags.Duration("interval", defaultSyncInterval, "sync interval")
	system := flags.Bool("system", false, "install as a system service instead of a user service")
	if err := flags.Parse(args); err != nil {
		return workerFlags{}, false, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tokitoki service options must appear after the action")
		return workerFlags{}, false, false
	}
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be greater than zero")
		return workerFlags{}, false, false
	}
	dirs := providerDirs.ProviderDirs()
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to scan; pass --provider-dir provider=dir")
		return workerFlags{}, false, false
	}
	return workerFlags{
		providerDirs: dirs,
		interval:     *interval,
	}, !*system, true
}

func newService(flags workerFlags, userService bool) (daemonservice.Service, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	config := &daemonservice.Config{
		Name:        "tokitoki",
		DisplayName: "TokiToki",
		Description: "Sync local AI usage to TokiToki.",
		Executable:  executable,
		Arguments:   serviceArguments(flags),
		Option: daemonservice.KeyValue{
			"UserService": userService,
			"Restart":     "always",
		},
	}
	return daemonservice.New(&serviceProgram{flags: flags}, config)
}

type providerDirFlags struct {
	dirs map[agentlib.Provider][]string
	set  bool
}

func newProviderDirFlags(defaults map[agentlib.Provider][]string) *providerDirFlags {
	return &providerDirFlags{dirs: copyProviderDirs(defaults)}
}

func (f *providerDirFlags) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(providerDirArgs(f.dirs), ",")
}

func (f *providerDirFlags) Set(value string) error {
	provider, dir, ok := strings.Cut(value, "=")
	provider = strings.TrimSpace(provider)
	dir = strings.TrimSpace(dir)
	if !ok || provider == "" || dir == "" {
		return fmt.Errorf("provider directory must use provider=dir")
	}
	if !f.set {
		f.dirs = make(map[agentlib.Provider][]string)
		f.set = true
	}
	f.dirs[agentlib.Provider(provider)] = append(f.dirs[agentlib.Provider(provider)], dir)
	return nil
}

func (f *providerDirFlags) ProviderDirs() map[agentlib.Provider][]string {
	if f == nil {
		return nil
	}
	return copyProviderDirs(f.dirs)
}

func serviceArguments(flags workerFlags) []string {
	args := []string{"__service-run"}
	for _, value := range providerDirArgs(flags.providerDirs) {
		args = append(args, "--provider-dir", value)
	}
	return append(args, "--interval", flags.interval.String())
}

func providerDirArgs(providerDirs map[agentlib.Provider][]string) []string {
	values := make([]string, 0)
	providers := make([]agentlib.Provider, 0, len(providerDirs))
	for provider := range providerDirs {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i] < providers[j]
	})
	for _, provider := range providers {
		dirs := append([]string{}, providerDirs[provider]...)
		sort.Strings(dirs)
		for _, dir := range dirs {
			if dir != "" {
				values = append(values, fmt.Sprintf("%s=%s", provider, dir))
			}
		}
	}
	return values
}

func copyProviderDirs(providerDirs map[agentlib.Provider][]string) map[agentlib.Provider][]string {
	copied := make(map[agentlib.Provider][]string, len(providerDirs))
	for provider, dirs := range providerDirs {
		for _, dir := range dirs {
			if dir != "" {
				copied[provider] = append(copied[provider], dir)
			}
		}
	}
	return copied
}

func serviceStatusString(status daemonservice.Status) string {
	switch status {
	case daemonservice.StatusRunning:
		return "running"
	case daemonservice.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `tokitoki — upload local AI usage to http://localhost:9093

Usage:
  tokitoki [--provider-dir PROVIDER=DIR ...]
  tokitoki set key <API_KEY>
  tokitoki get key
  tokitoki service <install|uninstall|start|stop|restart|status> [options]

Each invocation scans the directories you pass and uploads their usage events
to http://localhost:9093/api/usage-events/batch. By default, tokitoki scans
~/.claude and ~/.codex; pass one or more --provider-dir provider=dir values to
scan an explicit provider set. The API key is read from ~/.tokitoki/api_key.
Use tokitoki set key <API_KEY> to create or update that file. Set
TOKITOKI_BASE_URL to override the default base URL.

Examples:
  tokitoki set key tt_live_xxx
  tokitoki get key
  tokitoki
  tokitoki --provider-dir claude=~/.claude --provider-dir codex=~/.codex
  tokitoki service install
  tokitoki service status
`)
}

func defaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func fail(logger *slog.Logger, err error) int {
	logger.Error("tokitoki failed", "error", err)
	return 1
}

func writeJSON(out io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", data)
	return err
}
