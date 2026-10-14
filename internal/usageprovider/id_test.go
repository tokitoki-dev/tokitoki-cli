package usageprovider

import (
	"testing"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// What the CLI derives or where it read a record from must never reach an
// id: the server deduplicates on the id alone, so any of these changing
// would upload the same event twice.
func TestIDsIgnoreWhatTheCLIDerives(t *testing.T) {
	base := BaseEntry(usage.ProviderPi, time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), "pi", "--Users-dev-demo--",
		"session-1", "[pi] claude-sonnet-4", "pi-agent", usage.TokenUsage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150})
	SetSource(&base, "/old/place/session.jsonl", 3, 120, 240)

	derived := base
	derived.Project, derived.ProjectPath = "demo", "/Users/dev/demo"
	derived.Model = "claude-sonnet-4"
	SetSource(&derived, "/new/place/session.jsonl", 1, 0, 120)

	if MessageID(base, "msg_001") != MessageID(derived, "msg_001") {
		t.Fatal("MessageID changed with project, path, model or source position")
	}
	if ContentID(base) != ContentID(derived) {
		t.Fatal("ContentID changed with project, path, model or source position")
	}
}

func TestMessageIDIsTheAgentsOwnID(t *testing.T) {
	entry := BaseEntry(usage.ProviderKimi, time.Now(), "kimi", "Kimi", "session-1", "kimi-k2", "Kimi", usage.TokenUsage{TotalTokens: 1})
	if got, want := MessageID(entry, " msg-1 "), EventID(usage.ProviderKimi, "session-1", "msg-1"); got != want {
		t.Fatalf("MessageID = %s, want %s", got, want)
	}
	if MessageID(entry, "msg-1") == MessageID(entry, "msg-2") {
		t.Fatal("two messages share an id")
	}
	if got, want := MessageID(entry, "", "3"), ContentID(entry, "3"); got != want {
		t.Fatalf("id-less MessageID = %s, want ContentID %s", got, want)
	}
}
