package langdetect

import "testing"

func TestFromPathUsesFilenameAndExtensionRules(t *testing.T) {
	tests := map[string]string{
		"/repo/go.mod":          "Go",
		"/repo/app/page.tsx":    "TypeScript",
		"/repo/server/main.go":  "Go",
		"/repo/CMakeLists.txt":  "CMake",
		"/repo/Dockerfile":      "Docker",
		"/repo/README.md":       "Markdown",
		"/repo/include/util.h":  "C",
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

func TestDominantResolvesHeadersToContextLanguage(t *testing.T) {
	tests := []struct {
		name       string
		candidates []Candidate
		want       string
	}{
		{
			name: "headers inherit C++ from session",
			candidates: []Candidate{
				{Path: "/repo/src/engine.cpp", Weight: 1},
				{Path: "/repo/src/engine.h", Weight: 3},
				{Path: "/repo/src/render.h", Weight: 3},
			},
			want: "C++",
		},
		{
			name: "headers inherit C from session",
			candidates: []Candidate{
				{Path: "/repo/src/main.c", Weight: 1},
				{Path: "/repo/src/main.h", Weight: 3},
			},
			want: "C",
		},
		{
			name: "headers inherit Objective-C from session",
			candidates: []Candidate{
				{Path: "/repo/App/AppDelegate.m", Weight: 1},
				{Path: "/repo/App/AppDelegate.h", Weight: 3},
			},
			want: "Objective-C",
		},
		{
			name: "headers alone default to C",
			candidates: []Candidate{
				{Path: "/repo/include/util.h", Weight: 1},
			},
			want: "C",
		},
	}

	for _, tt := range tests {
		if got := Dominant(tt.candidates); got != tt.want {
			t.Fatalf("%s: Dominant = %q, want %q", tt.name, got, tt.want)
		}
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
