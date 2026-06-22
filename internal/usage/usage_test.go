package usage

import "testing"

func TestNormalizeClientUsesRealIDESource(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		raw      string
		want     string
	}{
		{
			name:     "claude vscode plugin",
			provider: ProviderClaude,
			raw:      "claude-vscode",
			want:     "VS Code",
		},
		{
			name:     "codex vscode plugin",
			provider: ProviderCodex,
			raw:      "codex_vscode",
			want:     "VS Code",
		},
		{
			name:     "codex cli",
			provider: ProviderCodex,
			raw:      "codex_cli_rs",
			want:     "Codex CLI",
		},
		{
			name:     "claude sdk cli",
			provider: ProviderClaude,
			raw:      "sdk-cli",
			want:     "Claude CLI",
		},
		{
			name:     "claude sdk",
			provider: ProviderClaude,
			raw:      "sdk-ts",
			want:     "Claude SDK",
		},
		{
			name:     "codex desktop",
			provider: ProviderCodex,
			raw:      "codex desktop",
			want:     "Codex Desktop",
		},
		{
			name:     "unknown preserved",
			provider: ProviderClaude,
			raw:      "Custom IDE",
			want:     "Custom IDE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeClient(tt.provider, tt.raw); got != tt.want {
				t.Fatalf("NormalizeClient(%q, %q) = %q, want %q", tt.provider, tt.raw, got, tt.want)
			}
		})
	}
}
