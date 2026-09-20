// Package logship forwards the ERROR lines of the run log to the server.
//
// The log file (internal/applog) is the only source. Nothing is reported from
// inside the code that fails: a failure logs at the level it deserves, and
// this package later carries the Error lines across. That keeps reporting out
// of every error path, and it gets three things an in-process hook cannot.
// Errors logged while offline are still in the file when the network returns.
// A run that was killed never reported anything, but the run after it reads
// what it wrote. And the many short processes that share the log — every
// editor's heartbeat, the sync, the service — are forwarded from one place,
// so one throttle and one dedupe cover all of them.
//
// Error is a narrow level in this program (see logFailure in cmd/tokitoki):
// an install broken in a way that will not fix itself and that the server
// cannot see. So what travels is faults, and few of them.
package logship

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/buildinfo"
	"github.com/tokitoki-dev/tokitoki-cli/internal/machineid"
	"github.com/tokitoki-dev/tokitoki-cli/internal/store"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

const (
	// Timeout bounds one forwarding attempt. It runs at the end of a command a
	// front-end is waiting on, so it must not be able to hold that up for long.
	Timeout = 10 * time.Second

	// interval is the least time between attempts, failed ones included. A
	// heartbeat fires every couple of minutes per editor; without this each
	// would re-read the log and knock on the server.
	interval = 10 * time.Minute

	// resendAfter is how long a fault stays quiet after it was reported. A
	// broken install fails the same way on every sync — 288 times a day — and
	// the first report says all there is to say. Repeats are counted and the
	// count rides on the next report.
	resendAfter = 24 * time.Hour

	// unfinishedAfter is how long a run may go without logging its finish
	// before it is taken for dead. Front-ends kill the CLI at 140 seconds.
	unfinishedAfter = 10 * time.Minute

	maxEvents       = 50
	maxContextLines = 20
	maxContextLine  = 1000

	stampFile   = "log-ship"
	cursorFile  = "log-cursor.json"
	shippedFile = "log-shipped.json"
)

// Options says where the log is and where its errors go.
type Options struct {
	DataDir string
	BaseURL string
	// APIKey puts a name to the report. Empty is fine: the server accepts an
	// anonymous report from an identified machine, as it does the install ping.
	APIKey string
	// Force skips the interval. Set when the previous run crashed: a crash
	// loop never reaches the end of a run, so its only chance to be reported
	// is at the start of the next one, whenever that is.
	Force bool
}

// state is what survives between passes.
type state struct {
	// Offset is where the last pass stopped reading; Head identifies the file
	// it belongs to. Rotation swaps the file under the same name, and an
	// offset into the old file means nothing in the new one — after a long
	// time offline the new file can even have grown past it, so size alone
	// cannot tell.
	Offset int64  `json:"offset"`
	Head   string `json:"head"`

	// Pending holds runs that logged a start and not yet a finish. It is kept
	// across passes because the start line is behind the cursor by the time
	// the run is overdue.
	Pending map[string]pendingRun `json:"pending,omitempty"`
}

type pendingRun struct {
	Time    time.Time `json:"time"`
	Cmd     string    `json:"cmd"`
	Version string    `json:"version"`
}

type shipped struct {
	At         time.Time `json:"at"`
	Suppressed int       `json:"suppressed,omitempty"`
}

type event struct {
	Time    string         `json:"time"`
	Cmd     string         `json:"cmd"`
	PID     int            `json:"pid"`
	Msg     string         `json:"msg"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Count   int            `json:"count"`
	Context []string       `json:"context,omitempty"`

	fingerprint string
}

type payload struct {
	MachineID  string  `json:"machine_id"`
	Platform   string  `json:"platform"`
	Arch       string  `json:"arch"`
	AppVersion string  `json:"app_version"`
	Events     []event `json:"events"`
}

// Ship forwards the log's new Error lines. It returns an error for the caller
// to log — below Error, or a failure to report errors would itself be an
// error to report, forever.
func Ship(ctx context.Context, opts Options) error {
	// Uploading switched off means nothing leaves this machine. Diagnostics
	// are not the usage data the switch was made for, but the person who
	// flipped it did not ask for an exception.
	if store.UploadDisabled(opts.DataDir) {
		return nil
	}

	stamp := store.StatePath(opts.DataDir, stampFile)
	if info, err := os.Stat(stamp); err == nil && !opts.Force && time.Since(info.ModTime()) < interval {
		return nil
	}
	// Stamped before the attempt, so a server that is down is asked again in
	// ten minutes, not on every heartbeat.
	if err := touch(stamp); err != nil {
		return err
	}

	var cursor state
	readJSON(store.StatePath(opts.DataDir, cursorFile), &cursor)
	sent := map[string]shipped{}
	readJSON(store.StatePath(opts.DataDir, shippedFile), &sent)

	now := time.Now()
	events, next, err := collect(store.LogPath(opts.DataDir, store.LogFile), cursor, now)
	if err != nil {
		return err
	}
	events = dedupe(events, sent, now)

	if len(events) > 0 {
		scrub(events, homeDir())
		if err := post(ctx, opts, events); err != nil {
			// Nothing is saved: the next pass reads the same lines again.
			return err
		}
	}

	if err := writeJSON(store.StatePath(opts.DataDir, cursorFile), next); err != nil {
		return err
	}
	return writeJSON(store.StatePath(opts.DataDir, shippedFile), sent)
}

// collect reads the log from the cursor and returns the Error lines found,
// the runs found dead, and the cursor to save if they are delivered.
func collect(path string, cursor state, now time.Time) ([]event, state, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, cursor, nil
	}
	if err != nil {
		return nil, cursor, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	first, err := reader.ReadBytes('\n')
	if err != nil {
		// Empty, or one line still being written. Nothing to read yet.
		return nil, cursor, nil
	}
	head := headOf(first)

	next := state{Offset: cursor.Offset, Head: head, Pending: map[string]pendingRun{}}
	for pid, run := range cursor.Pending {
		next.Pending[pid] = run
	}
	info, err := file.Stat()
	if err != nil {
		return nil, cursor, err
	}
	if cursor.Head != head || info.Size() < cursor.Offset {
		next.Offset = 0
	}
	if _, err := file.Seek(next.Offset, io.SeekStart); err != nil {
		return nil, cursor, err
	}
	reader.Reset(file)

	var events []event
	recent := map[int][]string{}
	for {
		raw, err := reader.ReadBytes('\n')
		if err != nil {
			// A line without its newline is still being written by another
			// process; it is left for the next pass, whole.
			break
		}
		next.Offset += int64(len(raw))

		var line map[string]any
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		pid := int(number(line["pid"]))
		msg, _ := line["msg"].(string)
		cmd, _ := line["cmd"].(string)
		key := fmt.Sprint(pid)

		switch msg {
		case "run started":
			// The same pid starting again means its last run is gone, however
			// recently it began: the OS does not hand a live pid out twice.
			if dead, ok := next.Pending[key]; ok {
				events = append(events, unfinished(pid, dead))
			}
			if longRunning(cmd) {
				delete(next.Pending, key)
			} else {
				when, _ := time.Parse(time.RFC3339Nano, text(line["time"]))
				next.Pending[key] = pendingRun{Time: when, Cmd: cmd, Version: text(line["version"])}
			}
		case "run finished":
			delete(next.Pending, key)
		}

		if line["level"] == "ERROR" {
			events = append(events, event{
				Time:    text(line["time"]),
				Cmd:     cmd,
				PID:     pid,
				Msg:     msg,
				Attrs:   attrsOf(line),
				Count:   1,
				Context: append([]string(nil), recent[pid]...),
			})
		}

		kept := strings.TrimRight(string(raw), "\r\n")
		if len(kept) > maxContextLine {
			kept = kept[:maxContextLine]
		}
		recent[pid] = append(recent[pid], kept)
		if len(recent[pid]) > maxContextLines {
			recent[pid] = recent[pid][1:]
		}
	}

	for key, run := range next.Pending {
		if now.Sub(run.Time) > unfinishedAfter {
			var pid int
			fmt.Sscan(key, &pid)
			events = append(events, unfinished(pid, run))
			delete(next.Pending, key)
		}
	}
	return events, next, nil
}

// unfinished is the report for a run that logged its start and never its
// finish: killed by a front-end's timeout, by the OS, or hung until something
// gave up on it. No line inside that run could have said so.
func unfinished(pid int, run pendingRun) event {
	return event{
		Time:  run.Time.UTC().Format(time.RFC3339Nano),
		Cmd:   run.Cmd,
		PID:   pid,
		Msg:   "run did not finish",
		Attrs: map[string]any{"error": "no finish line: the process was killed or hung", "version": run.Version},
		Count: 1,
	}
}

// longRunning commands start once and are not expected to finish.
func longRunning(cmd string) bool {
	return cmd == "__service-run" || cmd == "service"
}

// headOf identifies a log file by its first line's timestamp, which is fixed
// the moment the file is created and differs for every file rotation makes.
func headOf(firstLine []byte) string {
	var line struct {
		Time string `json:"time"`
	}
	if json.Unmarshal(firstLine, &line) == nil && line.Time != "" {
		return line.Time
	}
	sum := sha256.Sum256(firstLine)
	return hex.EncodeToString(sum[:8])
}

// attrsOf is the line minus what every line has. What remains is what this
// line had to say: error, provider, trace.
func attrsOf(line map[string]any) map[string]any {
	attrs := make(map[string]any, len(line))
	for key, value := range line {
		switch key {
		case "time", "level", "msg", "cmd", "pid":
		default:
			attrs[key] = value
		}
	}
	return attrs
}

var digits = regexp.MustCompile(`\d+`)

// fingerprint decides what counts as "the same fault again" on this machine,
// for the purpose of not resending it. The server groups by its own, stricter
// rule; this one only has to be stable here.
func fingerprint(e event) string {
	detail := text(e.Attrs["error"])
	if detail == "" {
		detail, _, _ = strings.Cut(text(e.Attrs["trace"]), "\n")
	}
	// The command is part of it: a sync that keeps getting killed and a
	// heartbeat that keeps getting killed are two problems, and the second
	// must not hide behind the first for a day.
	sum := sha256.Sum256([]byte(e.Cmd + "\n" + e.Msg + "\n" + digits.ReplaceAllString(detail, "#")))
	return hex.EncodeToString(sum[:8])
}

// dedupe folds repeats within this pass into one event with a count, and
// drops faults reported within resendAfter, remembering how many were dropped
// so the next report can say so.
func dedupe(events []event, sent map[string]shipped, now time.Time) []event {
	for fp, record := range sent {
		if now.Sub(record.At) > 7*24*time.Hour {
			delete(sent, fp)
		}
	}

	index := map[string]int{}
	var out []event
	for _, e := range events {
		e.fingerprint = fingerprint(e)
		if at, ok := index[e.fingerprint]; ok {
			out[at].Count++
			continue
		}
		index[e.fingerprint] = len(out)
		out = append(out, e)
	}

	kept := out[:0]
	for _, e := range out {
		record, seen := sent[e.fingerprint]
		if seen && now.Sub(record.At) < resendAfter {
			record.Suppressed += e.Count
			sent[e.fingerprint] = record
			continue
		}
		// Over the limit a fault is neither sent nor marked as sent. It is
		// dropped from this pass, and reported the next time it happens.
		if len(kept) == maxEvents {
			continue
		}
		e.Count += record.Suppressed
		sent[e.fingerprint] = shipped{At: now}
		kept = append(kept, e)
	}
	return kept
}

// scrub replaces the home directory with ~ everywhere it appears. What is
// left can still name a project or a file under it — an error about a path
// has to say which path — but not whose machine it is.
func scrub(events []event, home string) {
	if home == "" {
		return
	}
	// Context lines are raw JSON, where a Windows home is spelled with doubled
	// backslashes.
	escaped := strings.ReplaceAll(home, `\`, `\\`)
	clean := func(s string) string {
		s = strings.ReplaceAll(s, home, "~")
		if escaped != home {
			s = strings.ReplaceAll(s, escaped, "~")
		}
		return s
	}
	for i := range events {
		events[i].Msg = clean(events[i].Msg)
		events[i].Attrs = scrubValue(events[i].Attrs, clean).(map[string]any)
		for j, line := range events[i].Context {
			events[i].Context[j] = clean(line)
		}
	}
}

func scrubValue(value any, clean func(string) string) any {
	switch v := value.(type) {
	case string:
		return clean(v)
	case map[string]any:
		for key, inner := range v {
			v[key] = scrubValue(inner, clean)
		}
		return v
	case []any:
		for i, inner := range v {
			v[i] = scrubValue(inner, clean)
		}
		return v
	}
	return value
}

// post sends the report the way usage batches are sent: gzip JSON. A report
// is log lines and stack traces, which compress to about a tenth.
func post(ctx context.Context, opts Options, events []event) error {
	body, err := json.Marshal(payload{
		MachineID:  machineid.ID(),
		Platform:   usage.NormalizeOS(runtime.GOOS),
		Arch:       runtime.GOARCH,
		AppVersion: buildinfo.Resolved(),
		Events:     events,
	})
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(body); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	url := strings.TrimRight(opts.BaseURL, "/") + "/api/diagnostics"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &compressed)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("log forwarding refused: %s", resp.Status)
	}
	return nil
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func text(value any) string {
	s, _ := value.(string)
	return s
}

func number(value any) float64 {
	n, _ := value.(float64)
	return n
}

func touch(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	now := time.Now()
	return os.Chtimes(path, now, now)
}

// readJSON leaves target as it was when the file is missing or unreadable:
// state that cannot be read is state that starts over.
func readJSON(path string, target any) {
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, target)
	}
}

// writeJSON replaces path atomically, so a pass killed mid-write leaves the
// previous state, not half of the next.
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	staging := path + ".tmp"
	if err := os.WriteFile(staging, data, 0o600); err != nil {
		return err
	}
	return os.Rename(staging, path)
}
