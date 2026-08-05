package usage

import "testing"

func TestNormalizeClientReportsTheRawSource(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "claude vscode plugin",
			raw:  "claude-vscode",
			want: "claude-vscode",
		},
		{
			name: "codex vscode plugin",
			raw:  "codex_vscode",
			want: "codex_vscode",
		},
		{
			name: "codex cli",
			raw:  "codex_cli_rs",
			want: "codex_cli_rs",
		},
		{
			name: "claude sdk",
			raw:  "sdk-ts",
			want: "sdk-ts",
		},
		{
			name: "casing is preserved",
			raw:  "Codex Desktop",
			want: "Codex Desktop",
		},
		{
			name: "unknown preserved",
			raw:  "Custom IDE",
			want: "Custom IDE",
		},
		{
			name: "surrounding space trimmed",
			raw:  "  claude-desktop \n",
			want: "claude-desktop",
		},
		{
			name: "empty stays empty",
			raw:  "",
			want: "",
		},
		{
			name: "blank collapses to empty",
			raw:  "   ",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeClient(tt.raw); got != tt.want {
				t.Fatalf("NormalizeClient(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
