// Package applog is where a Tokitoki run writes down what it did.
//
// Every command used to log to stderr alone. The front-ends that launch the
// CLI read stderr for the one message they show and drop the rest, so a sync
// that failed on a user's machine left nothing behind: not on the machine,
// not on the server. The log file is the record that survives the process.
//
// Two sinks, two audiences. stderr keeps exactly what it always carried —
// text, Warn and above — because front-ends already parse around it. The file
// takes everything down to Debug as JSON lines: it is read after the fact, by
// someone who does not yet know which line matters, and rotation is what
// bounds it, not the level.
package applog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
)

const (
	maxSizeMB  = 5
	maxBackups = 3
	maxAgeDays = 30
)

// New returns a logger writing to stderr and to log/tokitoki.log under
// dataDir. attrs are stamped on every line of the file — the command, the
// version, the pid — so lines from the many short processes that share it
// can be told apart.
//
// It cannot fail. A log directory that cannot be created costs the file, not
// the command: the logger falls back to stderr alone.
func New(dataDir string, stderr io.Writer, attrs ...slog.Attr) *slog.Logger {
	console := slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn})

	path := store.LogPath(dataDir, store.LogFile)
	if dataDir == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return slog.New(console)
	}

	// Lumberjack documents itself as single-writer, and this file has many:
	// every editor's heartbeat, the sync, the service. What that costs here is
	// cosmetic. Each line is one O_APPEND write, so lines never interleave;
	// each process counts only its own bytes toward the limit, so the file can
	// overshoot it; and two processes rotating at once leave an extra small
	// backup. Nothing is lost, and none of it is worth a lock on the hot path
	// of every heartbeat.
	file := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
		MaxAge:     maxAgeDays,
	}
	record := slog.NewJSONHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug}).
		WithAttrs(attrs)

	return slog.New(fanout{console, record})
}

// maxCrashBytes bounds how much of a crash report is copied into the log. A
// Go traceback lists every goroutine; the panicking one comes first, and that
// is the part that names the bug.
const maxCrashBytes = 32 << 10

// CaptureCrashes makes a fatal panic leave a trace, and reports the one the
// previous run left.
//
// A panic is the one failure that cannot log itself. recover() in main would
// not help: a sync does its work in goroutines, and a panic there never
// unwinds through main. The runtime, though, can be told to copy its fatal
// output — any goroutine, and the errors recover cannot catch at all, like
// concurrent map writes — to a file, so that is what this does.
//
// The runtime writes raw text to a raw descriptor: no handler runs, and a
// traceback dropped into a log of JSON lines would break it. So the trace is
// staged in crash.log beside it, and the next run folds it into the log as
// one Error line and empties it. The log stays the single record of
// everything that went wrong, crashes included — which matters to anything
// that later reads it for Error lines, because a crash is otherwise the one
// error that never becomes one.
//
// It reports whether the previous run had crashed. A caller that forwards the
// log wants to know: a crash loop never reaches the end of a run, so the start
// of the next one is the only moment its crash can be sent anywhere.
func CaptureCrashes(dataDir string, logger *slog.Logger) (crashed bool) {
	if dataDir == "" {
		return false
	}
	path := store.LogPath(dataDir, store.CrashFile)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return false
	}

	// Two runs starting together can both read the same report and log it
	// twice. That is a duplicate line, which costs nothing; claiming the file
	// atomically would mean renaming it, which Windows refuses while the
	// service still has it open.
	if trace, err := os.ReadFile(path); err == nil && len(trace) > 0 {
		if len(trace) > maxCrashBytes {
			trace = trace[:maxCrashBytes]
		}
		logger.Error("previous run crashed", "trace", string(trace))
		_ = os.Truncate(path, 0)
		crashed = true
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return crashed
	}
	// SetCrashOutput duplicates the descriptor, so ours can go.
	_ = debug.SetCrashOutput(file, debug.CrashOptions{})
	_ = file.Close()
	return crashed
}

// fanout hands each record to every handler that wants its level.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, record slog.Record) error {
	for _, h := range f {
		if h.Enabled(ctx, record.Level) {
			// A sink that fails must not silence the others, and a log line
			// that cannot be written is nobody's error to handle.
			_ = h.Handle(ctx, record.Clone())
		}
	}
	return nil
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make(fanout, len(f))
	for i, h := range f {
		next[i] = h.WithAttrs(attrs)
	}
	return next
}

func (f fanout) WithGroup(name string) slog.Handler {
	next := make(fanout, len(f))
	for i, h := range f {
		next[i] = h.WithGroup(name)
	}
	return next
}
