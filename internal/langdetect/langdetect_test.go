package langdetect

import "testing"

func TestFromPathUsesWakaTimeStyleFilenameAndExtensionRules(t *testing.T) {
	tests := map[string]string{
		"/repo/go.mod":          "Go",
		"/repo/app/page.tsx":    "TypeScript",
		"/repo/server/main.go":  "Go",
		"/repo/CMakeLists.txt":  "CMake",
		"/repo/Dockerfile":      "Docker",
		"/repo/README.md":       "Markdown",
		"/repo/unknown.nopeext": Unknown,
	}

	for path, want := range tests {
		if got := FromPath(path); got != want {
			t.Fatalf("FromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDominantWeightsCandidates(t *testing.T) {
	got := Dominant([]Candidate{
		{Path: "/repo/README.md", Weight: 1},
		{Path: "/repo/app/page.tsx", Weight: 3},
		{Path: "/repo/lib/api.ts", Weight: 1},
	})
	if got != "TypeScript" {
		t.Fatalf("Dominant = %q, want TypeScript", got)
	}
}

func TestPathsFromTextExtractsKnownFilePaths(t *testing.T) {
	paths := PathsFromText(`sed -n '1,20p' internal/httpapi/server.go && cat app/page.tsx`)
	if len(paths) != 2 {
		t.Fatalf("len(paths) = %d, want 2 (%v)", len(paths), paths)
	}
	if paths[0] != "internal/httpapi/server.go" || paths[1] != "app/page.tsx" {
		t.Fatalf("paths = %v, want go and tsx paths", paths)
	}
}
