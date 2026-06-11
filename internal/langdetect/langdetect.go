package langdetect

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const Unknown = "Unknown"

type Candidate struct {
	Path   string
	Weight int
}

var pathTokenRE = regexp.MustCompile(`(?i)(?:^|[\s"'` + "`" + `([{<])([~./A-Z0-9_@{}$%:+,-][^\s"'` + "`" + `(){}[\]<>|;]*\.(?:[A-Z0-9_+-]{1,16})(?:\b|$))`)

var filenameLanguages = map[string]string{
	".bashrc":         "Bash",
	".bash_history":   "Bash",
	".gvimrc":         "VimL",
	".htaccess":       "Apache Config",
	".rhistory":       "S",
	".renviron":       "S",
	".rprofile":       "S",
	".ruby-version":   "Ruby",
	".vimrc":          "VimL",
	".zshrc":          "Bash",
	"buck":            "Python",
	"build":           "Python",
	"build.bazel":     "Python",
	"cmakelists.txt":  "CMake",
	"dockerfile":      "Docker",
	"gemfile":         "Ruby",
	"go.mod":          "Go",
	"go.sum":          "Go",
	"gnumakefile":     "Makefile",
	"gruntfile":       "JavaScript",
	"makefile":        "Makefile",
	"pipfile":         "TOML",
	"pkgbuild":        "Bash",
	"poetry.lock":     "TOML",
	"rakefile":        "Ruby",
	"sconscript":      "Python",
	"sconstruct":      "Python",
	"vimrc":           "VimL",
	"workspace":       "Python",
	"workspace.bazel": "Python",
	"zshrc":           "Bash",
}

var extensionLanguages = map[string]string{
	".abap":             "ABAP",
	".adb":              "Ada",
	".ads":              "Ada",
	".agda":             "Agda",
	".applescript":      "AppleScript",
	".astro":            "Astro",
	".awk":              "Awk",
	".bash":             "Bash",
	".bat":              "Batchfile",
	".bats":             "Bash",
	".bazel":            "Python",
	".bzl":              "Python",
	".c":                "C",
	".c++":              "C++",
	".cabal":            "Cabal Config",
	".cc":               "C++",
	".cfg":              "INI",
	".clj":              "Clojure",
	".cljs":             "ClojureScript",
	".cmake":            "CMake",
	".coffee":           "CoffeeScript",
	".conf":             "INI",
	".cpp":              "C++",
	".cr":               "Crystal",
	".cs":               "C#",
	".css":              "CSS",
	".csv":              "CSV",
	".cxx":              "C++",
	".dart":             "Dart",
	".docker":           "Docker",
	".dpr":              "Object Pascal",
	".dts":              "Devicetree",
	".dtsi":             "Devicetree",
	".eex":              "Elixir",
	".el":               "EmacsLisp",
	".elm":              "Elm",
	".erl":              "Erlang",
	".ex":               "Elixir",
	".exs":              "Elixir",
	".fs":               "F#",
	".fsi":              "F#",
	".gemspec":          "Ruby",
	".go":               "Go",
	".gradle":           "Groovy",
	".graphql":          "GraphQL",
	".groovy":           "Groovy",
	".gsp":              "Gosu",
	".gs":               "Gosu",
	".h":                "C/C++ Header",
	".h++":              "C++",
	".haml":             "Haml",
	".hh":               "C++",
	".hpp":              "C++",
	".hrl":              "Erlang",
	".hs":               "Haskell",
	".html":             "HTML",
	".htm":              "HTML",
	".hxx":              "C++",
	".i":                "SWIG",
	".ini":              "INI",
	".java":             "Java",
	".jl":               "Julia",
	".js":               "JavaScript",
	".json":             "JSON",
	".jsonl":            "JSON",
	".jsx":              "JSX",
	".kt":               "Kotlin",
	".kts":              "Kotlin",
	".less":             "Less",
	".liquid":           "Liquid",
	".lua":              "Lua",
	".m":                "Objective-C",
	".markdown":         "Markdown",
	".md":               "Markdown",
	".mdx":              "MDX",
	".mjs":              "JavaScript",
	".ml":               "OCaml",
	".mli":              "OCaml",
	".mm":               "Objective-C++",
	".nix":              "Nix",
	".pas":              "Object Pascal",
	".pbxproj":          "Xcode Project",
	".php":              "PHP",
	".php3":             "PHP",
	".php4":             "PHP",
	".php5":             "PHP",
	".pl":               "Perl",
	".pm":               "Perl",
	".prisma":           "Prisma",
	".pro":              "Prolog",
	".proto":            "Protocol Buffer",
	".ps1":              "PowerShell",
	".psm1":             "PowerShell",
	".pug":              "Pug",
	".py":               "Python",
	".pyw":              "Python",
	".qml":              "QML",
	".r":                "S",
	".rake":             "Ruby",
	".rb":               "Ruby",
	".re":               "ReasonML",
	".rei":              "ReasonML",
	".rs":               "Rust",
	".rst":              "ReStructuredText",
	".sass":             "Sass",
	".scala":            "Scala",
	".scm":              "Scheme",
	".scss":             "SCSS",
	".sh":               "Bash",
	".sql":              "SQL",
	".sublime-settings": "Sublime Text Config",
	".svelte":           "Svelte",
	".sv":               "SystemVerilog",
	".svh":              "SystemVerilog",
	".swift":            "Swift",
	".toml":             "TOML",
	".ts":               "TypeScript",
	".tsx":              "TypeScript",
	".twig":             "Twig",
	".txt":              "Text",
	".vb":               "VB.Net",
	".vcl":              "VCL",
	".vim":              "VimL",
	".vue":              "Vue.js",
	".xaml":             "XAML",
	".xml":              "XML",
	".yaml":             "YAML",
	".yml":              "YAML",
	".zig":              "Zig",
	".zsh":              "Bash",
}

func FromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return Unknown
	}

	base := strings.ToLower(filepath.Base(strings.Trim(path, `"'`)))
	if language, ok := filenameLanguages[base]; ok {
		return language
	}

	ext := strings.ToLower(filepath.Ext(base))
	if language, ok := extensionLanguages[ext]; ok {
		return language
	}

	return Unknown
}

func DominantFromPaths(paths []string) string {
	candidates := make([]Candidate, 0, len(paths))
	for _, path := range paths {
		candidates = append(candidates, Candidate{Path: path, Weight: 1})
	}
	return Dominant(candidates)
}

func Dominant(candidates []Candidate) string {
	type score struct {
		language string
		weight   int
	}

	scores := map[string]int{}
	for _, candidate := range candidates {
		language := FromPath(candidate.Path)
		if language == Unknown {
			continue
		}
		weight := candidate.Weight
		if weight <= 0 {
			weight = 1
		}
		scores[language] += weight
	}

	if len(scores) == 0 {
		return Unknown
	}

	ranked := make([]score, 0, len(scores))
	for language, weight := range scores {
		ranked = append(ranked, score{language: language, weight: weight})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].weight != ranked[j].weight {
			return ranked[i].weight > ranked[j].weight
		}
		return ranked[i].language < ranked[j].language
	})
	return ranked[0].language
}

func PathsFromText(text string) []string {
	matches := pathTokenRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	paths := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := cleanPathToken(match[1])
		if path == "" || FromPath(path) == Unknown {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func cleanPathToken(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"'`)
	path = strings.TrimRight(path, ".,:")
	for {
		trimmed := strings.TrimRight(path, ")]}")
		if trimmed == path {
			break
		}
		path = trimmed
	}
	return path
}
