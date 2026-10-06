// Package shellcmd reads which programs a shell command line runs.
//
// It parses rather than splits. A command line is a grammar — quotes, heredocs,
// pipelines, loops, $(...) — and every way of cutting it with string
// operations reports words that never ran: the `|` inside `grep "a|b"`, the
// `import` in a heredoc body, the pattern of a sed expression. Measured on
// 19,000 real agent commands before this was written, splitting got the
// commonest of those wrong on every second call.
package shellcmd

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Programs returns the command names cmd runs, in the order they first appear,
// each once. A command name is what the shell itself executes: the first word
// of each simple command anywhere in the line, including inside pipelines,
// lists, loops, conditionals and command substitutions. Arguments are never
// programs, so `xargs grep` and `timeout 5 curl` name xargs and timeout — the
// shell does not run grep or curl, they do.
//
// `sh -c '<script>'` (and bash, zsh) is followed into the script, because that
// is where the work is; the shell itself is not reported.
//
// A name computed at run time ($TOOL, "$(which x)") is not a name this can
// know and is left out. A line that does not parse — an unclosed quote, which
// the shell would reject as well — yields nil.
func Programs(cmd string) []string {
	file := parse(cmd)
	if file == nil {
		return nil
	}
	seen := map[string]bool{}
	programs := []string{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			programs = append(programs, name)
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		name := baseName(literal(call.Args[0]))
		if script, ok := shellScript(name, call.Args); ok {
			for _, inner := range Programs(script) {
				add(inner)
			}
			return true
		}
		add(name)
		return true
	})
	return programs
}

// parse reads cmd as bash, then as zsh. Codex runs commands under zsh, which
// accepts words bash rejects — an unquoted Next.js route such as
// app/(auth)/page.tsx is a glob to zsh and a syntax error to bash.
func parse(cmd string) *syntax.File {
	if file, err := syntax.NewParser().Parse(strings.NewReader(cmd), ""); err == nil {
		return file
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangZsh)).Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil
	}
	return file
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true}

// shellScript reports the script of `sh -c '<script>'`, `zsh -lc ...` and the
// like: a shell, a flag cluster ending in c, then the script.
func shellScript(name string, args []*syntax.Word) (string, bool) {
	if !shells[name] || len(args) < 3 {
		return "", false
	}
	flags := literal(args[1])
	if !strings.HasPrefix(flags, "-") || !strings.HasSuffix(flags, "c") {
		return "", false
	}
	return literal(args[2]), true
}

// literal is a word's value when it is made only of literal text — plain,
// 'single-' or "double-quoted" — so 'git' names git exactly as git does. A
// word with any expansion in it is "": its value is decided at run time.
func literal(word *syntax.Word) string {
	var b strings.Builder
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return ""
				}
				b.WriteString(lit.Value)
			}
		default:
			return ""
		}
	}
	return b.String()
}

// baseName drops a directory: /usr/bin/rg runs rg.
func baseName(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// ArgvProgram is the program of a command given as an argument vector rather
// than a shell line — ["git", "status"] — which is how some agents record a
// call. A vector that is itself a shell running a script, ["zsh", "-lc",
// "<script>"], is read as that script.
func ArgvProgram(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	name := baseName(argv[0])
	if shells[name] && len(argv) >= 3 && strings.HasPrefix(argv[1], "-") && strings.HasSuffix(argv[1], "c") {
		return Programs(argv[2])
	}
	if name == "" {
		return nil
	}
	return []string{name}
}
