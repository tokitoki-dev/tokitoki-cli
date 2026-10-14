package shellcmd

import (
	"reflect"
	"testing"
)

func TestPrograms(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"single", "ls -la", []string{"ls"}},
		{"pipeline", `rg -n "a|b" src | head -5`, []string{"rg", "head"}},
		{"list", "cd x && go test ./... ; git status", []string{"cd", "go", "git"}},
		{"path and env prefix", "FOO=1 /usr/bin/rg x", []string{"rg"}},
		{"heredoc body is not a command", "python3 - <<'EOF'\nimport os\nconst x = 1\nEOF", []string{"python3"}},
		{"loop body", "for f in *.go; do grep -l TODO $f; done", []string{"grep"}},
		{"command substitution", "echo $(git rev-parse HEAD)", []string{"echo", "git"}},
		{"time keyword", "time go test ./...", []string{"go"}},
		{"each once in order", "sed -n 1p a; rg x; sed -n 2p b", []string{"sed", "rg"}},
		{"quoted names", `'git' 'status' '--short'`, []string{"git"}},
		{"nested shell script", `zsh -lc 'rg x | sed -n 1p'`, []string{"rg", "sed"}},
		{"bash -c", `bash -c "go vet && git diff"`, []string{"go", "git"}},
		{"arguments are not programs", "find . | xargs grep foo", []string{"find", "xargs"}},
		{"zsh-only word", "sed -n '1,40p' app/(auth)/page.tsx", []string{"sed"}},
		{"computed name", "$TK help up", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Programs(c.cmd); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Programs(%q) = %#v, want %#v", c.cmd, got, c.want)
			}
		})
	}
}

func TestProgramsUnparseable(t *testing.T) {
	if got := Programs(`echo "unclosed`); got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

func TestArgvProgram(t *testing.T) {
	cases := []struct {
		argv []string
		want []string
	}{
		{[]string{"apply_patch", "*** Begin Patch"}, []string{"apply_patch"}},
		{[]string{"/bin/zsh", "-lc", "rg x && nl -ba f"}, []string{"rg", "nl"}},
		{nil, nil},
	}
	for _, c := range cases {
		if got := ArgvProgram(c.argv); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("ArgvProgram(%q) = %#v, want %#v", c.argv, got, c.want)
		}
	}
}
