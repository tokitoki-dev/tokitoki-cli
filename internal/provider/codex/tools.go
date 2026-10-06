package codex

import (
	"encoding/json"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/shellcmd"
	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// Tool calls in a codex rollout.
//
// Codex has recorded a call three ways as it changed, and a rollout of any age
// may hold any of them:
//
//   - a function_call whose name says it all: "exec_command", or
//     "mcp__supabase__execute_sql" for an MCP tool;
//   - a function_call with the server split into a namespace: name "js",
//     namespace "mcp__node_repl";
//   - code mode: one custom_tool_call named "exec" whose input is a script,
//     with the tools the script ran recorded only as item_completed events.
//
// item_completed is also written for calls of the first two shapes, under the
// same id as the function_call's call_id. So one rule covers every shape: a
// call is keyed on its id, an item for an id this file has already invoked is
// that call's outcome, and an item for any other id is a call of its own —
// the code-mode case, where the item is the only record there is.
//
// An outcome is emitted only when it can be read: a status from the item, the
// patch event, or the exit code codex writes into a shell call's output. A
// call whose outcome cannot be read has none, rather than a guessed one.

// shellTools are the tools that run a command line, under each name codex has
// given them.
var shellTools = map[string]bool{
	"exec_command":  true,
	"shell_command": true,
	"shell":         true,
	"local_shell":   true,
}

// toolEntriesFromResponseItem reads a response_item: a call is an invocation
// half, a call's output may be its outcome half.
func toolEntriesFromResponseItem(envelope codexLine, state *fileState) []usage.Entry {
	var item struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Namespace string          `json:"namespace"`
		Arguments json.RawMessage `json:"arguments"`
		Action    json.RawMessage `json:"action"`
		Output    json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(envelope.Payload, &item); err != nil {
		return nil
	}
	switch item.Type {
	case "function_call", "custom_tool_call", "local_shell_call", "tool_search_call", "web_search_call":
		// A web search carries its id as `id` and nothing else.
		callID := item.CallID
		if callID == "" {
			callID = item.ID
		}
		call := responseToolCall(item.Type, item.Name, item.Namespace, item.Arguments, item.Action)
		if callID == "" || call == nil {
			return nil
		}
		call.CallID = callID
		return state.toolCall(envelope.Timestamp, call, 0)
	case "function_call_output", "custom_tool_call_output":
		status, ok := outputStatus(state.toolCalls[item.CallID], item.Output)
		if !ok {
			return nil
		}
		return state.toolResult(envelope.Timestamp, item.CallID, status)
	}
	return nil
}

// responseToolCall names a response_item call. Nil when it names nothing.
func responseToolCall(itemType, name, namespace string, arguments, action json.RawMessage) *usage.ToolCall {
	switch itemType {
	case "web_search_call":
		return &usage.ToolCall{Name: "web_search"}
	case "tool_search_call":
		return &usage.ToolCall{Name: "tool_search"}
	case "local_shell_call":
		var local struct {
			Command json.RawMessage `json:"command"`
		}
		_ = json.Unmarshal(action, &local)
		return &usage.ToolCall{Name: "local_shell", Programs: commandPrograms(local.Command)}
	}
	if name == "" {
		return nil
	}

	call := &usage.ToolCall{Name: name}
	switch {
	case strings.HasPrefix(namespace, "mcp__"):
		// "mcp__node_repl", and in some versions "mcp__node_repl__".
		if server := strings.TrimRight(strings.TrimPrefix(namespace, "mcp__"), "_"); server != "" {
			call.MCPServer = server
		}
	case namespace != "":
		// Codex's own grouped tools: web.run, collaboration.spawn_agent.
		call.Name = namespace + "." + name
	case strings.HasPrefix(name, "mcp__"):
		server, tool, ok := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
		if ok && server != "" && tool != "" {
			call.MCPServer, call.Name = server, tool
		}
	}
	if call.MCPServer == "" && shellTools[call.Name] {
		call.Programs = argumentPrograms(arguments)
	}
	return call
}

// argumentPrograms reads the programs out of a shell call's arguments, which
// codex stores as a JSON document inside a JSON string. The command is `cmd`
// for exec_command and `command` for the others, a line or an argv.
func argumentPrograms(arguments json.RawMessage) []string {
	var text string
	if err := json.Unmarshal(arguments, &text); err != nil {
		return nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &args); err != nil {
		return nil
	}
	command := args["cmd"]
	if len(command) == 0 {
		command = args["command"]
	}
	return commandPrograms(command)
}

// commandPrograms reads a command recorded either as a shell line or as an
// argument vector.
func commandPrograms(raw json.RawMessage) []string {
	var line string
	if err := json.Unmarshal(raw, &line); err == nil {
		return shellcmd.Programs(line)
	}
	var argv []string
	if err := json.Unmarshal(raw, &argv); err == nil {
		return shellcmd.ArgvProgram(argv)
	}
	return nil
}

// exitCodeLine matches the exit status codex writes into a shell call's
// output header, in both its spellings.
var exitCodeLine = regexp.MustCompile(`(?m)^(?:Exit code:|Process exited with code) (\d+)\s*$`)

// outputStatus reads a call's outcome from its output, for the tools whose
// output codex stamps: a shell call's exit code and apply_patch's verdict.
// Any other output is the tool's own text, and its outcome is not read from it.
func outputStatus(tool string, output json.RawMessage) (string, bool) {
	text, exitCode, ok := outputText(output)
	if !ok {
		return "", false
	}
	switch {
	case shellTools[tool]:
		return shellStatus(text, exitCode)
	case tool == "apply_patch":
		return statusOf(outputSucceeded(output)), true
	}
	return "", false
}

func shellStatus(text string, exitCode *int) (string, bool) {
	// Older rollouts store {"output":…,"metadata":{"exit_code":…}} as a
	// string.
	if exitCode == nil && strings.HasPrefix(strings.TrimSpace(text), "{") {
		var structured struct {
			Metadata struct {
				ExitCode *int `json:"exit_code"`
			} `json:"metadata"`
		}
		if json.Unmarshal([]byte(text), &structured) == nil {
			exitCode = structured.Metadata.ExitCode
		}
	}
	if exitCode != nil {
		return statusOf(*exitCode == 0), true
	}
	// Only the header codex writes above the command's own output: the
	// command may well print "Exit code: 1" itself.
	header, _, _ := strings.Cut(text, "\nOutput:")
	if match := exitCodeLine.FindStringSubmatch(header); match != nil {
		return statusOf(match[1] == "0"), true
	}
	// "Process running with session ID …": the command outlived the call,
	// and its exit lands on a later write_stdin, not here.
	return "", false
}

var itemStatuses = map[string]string{
	"completed": usage.ToolStatusOK,
	"failed":    usage.ToolStatusError,
	"declined":  usage.ToolStatusError,
}

// toolEntriesFromItem reads an item_completed. An item for a call this file
// already invoked is that call's outcome; any other tool item is a code-mode
// call, recorded nowhere else, and is both halves at once.
func toolEntriesFromItem(timestamp string, raw json.RawMessage, state *fileState) []usage.Entry {
	var item struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Status  string          `json:"status"`
		Server  string          `json:"server"`
		Tool    string          `json:"tool"`
		Kind    string          `json:"kind"`
		Command json.RawMessage `json:"command"`
		// Raw: its shape is the item type's business.
		Duration json.RawMessage `json:"duration"`
	}
	if err := json.Unmarshal(raw, &item); err != nil || item.ID == "" {
		return nil
	}
	call := itemToolCall(item.Type, item.Server, item.Tool, item.Kind, item.Command)
	if call == nil {
		return nil
	}
	call.CallID = item.ID

	var entries []usage.Entry
	if _, seen := state.toolCalls[item.ID]; !seen {
		// Logged when it finished; its duration puts the start back where
		// it was.
		entries = append(entries, state.toolCall(timestamp, call, itemDuration(item.Duration))...)
	}
	if status, ok := itemStatuses[item.Status]; ok {
		entries = append(entries, state.toolResult(timestamp, item.ID, status)...)
	}
	return entries
}

// itemToolCall names an item_completed tool item. Nil for every other item —
// messages, reasoning, and SubAgentActivity, which reports on a spawn_agent
// call already recorded under the same id.
func itemToolCall(itemType, server, tool, kind string, command json.RawMessage) *usage.ToolCall {
	switch itemType {
	case "CommandExecution":
		return &usage.ToolCall{Name: "exec_command", Programs: commandPrograms(command)}
	case "McpToolCall":
		if tool == "" {
			return nil
		}
		return &usage.ToolCall{MCPServer: server, Name: tool}
	case "FileChange":
		return &usage.ToolCall{Name: "apply_patch"}
	case "WebSearch":
		return &usage.ToolCall{Name: "web_search"}
	case "ImageView":
		return &usage.ToolCall{Name: "view_image"}
	case "Extension":
		if kind == "" {
			return nil
		}
		return &usage.ToolCall{Name: kind}
	}
	return nil
}

// itemDuration reads codex's {"secs":…,"nanos":…}. Zero when absent or
// shaped otherwise.
func itemDuration(raw json.RawMessage) time.Duration {
	var duration struct {
		Secs  int64 `json:"secs"`
		Nanos int64 `json:"nanos"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &duration) != nil || duration.Secs < 0 || duration.Nanos < 0 {
		return 0
	}
	return time.Duration(duration.Secs)*time.Second + time.Duration(duration.Nanos)
}

// toolCall records an invocation half, started ran before timestamp.
func (state *fileState) toolCall(timestamp string, call *usage.ToolCall, ran time.Duration) []usage.Entry {
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return nil
	}
	if state.toolCalls == nil {
		state.toolCalls = make(map[string]string)
	}
	state.toolCalls[call.CallID] = call.Name
	entry := state.toolEntry(usage.EventKindToolCall, at.Add(-ran), call)
	entry.Model = state.model
	return []usage.Entry{entry}
}

// toolResult records an outcome half — only for a call this file invoked. An
// outcome with no invocation would be a row the server can never name.
func (state *fileState) toolResult(timestamp, callID, status string) []usage.Entry {
	if _, seen := state.toolCalls[callID]; !seen {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return nil
	}
	return []usage.Entry{state.toolEntry(usage.EventKindToolResult, at, &usage.ToolCall{CallID: callID, Status: status})}
}

func (state *fileState) toolEntry(kind string, at time.Time, call *usage.ToolCall) usage.Entry {
	return usage.Entry{
		Provider:    usage.ProviderCodex,
		ID:          usage.StableID(string(usage.ProviderCodex), kind, call.CallID),
		EventKind:   kind,
		Timestamp:   at,
		Date:        at.In(time.Local).Format("2006-01-02"),
		Project:     projectName(state.projectPath),
		ProjectPath: state.projectPath,
		SessionID:   state.sessionID,
		Language:    stateLanguage(state),
		OS:          usage.NormalizeOS(runtime.GOOS),
		Client:      state.client,
		Tool:        call,
	}
}

func statusOf(ok bool) string {
	if ok {
		return usage.ToolStatusOK
	}
	return usage.ToolStatusError
}
