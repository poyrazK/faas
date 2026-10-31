package sharedruntime

import (
	"path"
	"slices"
	"strings"
	"unicode"
)

// Reasons a Python start command that runs through a package manager keeps
// the full layer (ADR-1500 amendment 3).
const (
	// ReasonRunTargetUnresolved: the command `<tool> run` starts is not the
	// virtualenv's Python, one of its scripts or a path, so the tool may
	// resolve it itself (a Poetry, PDM or Pipfile script).
	ReasonRunTargetUnresolved = "run_target_unresolved"
	// ReasonPipenvDotEnv: `pipenv run` loads /app/.env, which nothing on the
	// runner base would.
	ReasonPipenvDotEnv = "pipenv_dotenv"
	// ReasonInlineScriptMetadata: `uv run script.py` gives a script with
	// inline metadata (PEP 723) an environment of its own.
	ReasonInlineScriptMetadata = "inline_script_metadata"
)

// PythonLayer is what imaged found in a Python app's /app-only layer. A
// package manager's `run` is dropped only when this shows that running the
// command with the virtualenv first on PATH is all the tool would do.
type PythonLayer struct {
	// Scripts are the names in /app/.venv/bin.
	Scripts map[string]bool
	// InlineScripts are the base names of .py files under /app with a
	// `# /// script` block.
	InlineScripts map[string]bool
	// DotEnv reports an /app/.env file.
	DotEnv bool
}

// pythonRunTools are the package managers whose `run` starts a command in
// /app/.venv, where Railpack 0.38 installs the project for each of them.
// The flags listed for a tool change nothing once the build has synced that
// environment; any other flag keeps the full layer.
var pythonRunTools = map[string]map[string]bool{
	"uv": {
		"--frozen": true, "--locked": true, "--no-sync": true, "--no-dev": true, "--exact": true,
		"--offline": true, "--quiet": true, "-q": true, "--no-progress": true, "--compile-bytecode": true,
		"--active": true, "--no-env-file": true, "--native-tls": true, "--no-cache": true, "-n": true,
	},
	"poetry": {"--no-interaction": true, "-n": true, "--no-ansi": true, "--quiet": true, "-q": true, "--no-plugins": true, "--no-cache": true},
	"pdm":    {"--quiet": true, "-q": true},
	"pipenv": {"--quiet": true, "-q": true},
}

// ResolvePythonStart turns a Python app's start command into an argv for the
// runner base, which ships no package manager. A `uv run`, `poetry run`,
// `pdm run` or `pipenv run` in front of a command is dropped, since the
// virtualenv's scripts are first on PATH (ADR-1500 amendment 3), when layer
// shows the tool would do nothing else. layer is nil where the app layer is
// not known yet (the builder's verdict); the run target is then not checked.
// Any other package manager, an unlisted flag, or shell syntax this does not
// follow around one keeps the full layer.
func ResolvePythonStart(command string, layer *PythonLayer) ([]string, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, ReasonNoStartCommand
	}
	commands, ok := splitShellCommands(command)
	if !ok {
		if mentionsPythonPackageManager(command) {
			return nil, ReasonPackageManagerStart
		}
		return []string{"/bin/sh", "-c", command}, ""
	}
	var edits []shellEdit
	for _, words := range commands {
		edit, reason := pythonRunEdit(words, layer)
		if reason != "" {
			return nil, reason
		}
		if edit != nil {
			edits = append(edits, *edit)
		}
	}
	for i := len(edits) - 1; i >= 0; i-- {
		command = command[:edits[i].start] + edits[i].text + command[edits[i].end:]
	}
	return []string{"/bin/sh", "-c", command}, ""
}

// pythonRunEdit drops a package manager's `run` from one simple command, or
// says why it cannot. It returns neither for a command without one.
func pythonRunEdit(words []shellWord, layer *PythonLayer) (*shellEdit, string) {
	i := 0
	for i < len(words) && shellAssignment(words[i].text) {
		i++
	}
	if i < len(words) && words[i].text == "exec" {
		i++
	}
	if i == len(words) || !PythonPackageManager(words[i].text) {
		return nil, ""
	}
	tool := path.Base(words[i].text)
	flags, ok := pythonRunTools[tool]
	if !ok {
		return nil, ReasonPackageManagerStart
	}
	j, run := i+1, false
scan:
	for ; j < len(words); j++ {
		switch w := words[j].text; {
		case !run && w == "run":
			run = true
		case flags[w]:
		case run && w == "--":
			j++
			break scan
		default:
			break scan
		}
	}
	if !run || j == len(words) {
		return nil, ReasonPackageManagerStart
	}
	target := words[j].text
	edit := &shellEdit{start: words[i].start, end: words[j].start}
	switch {
	case tool == "uv" && (target == "-m" || target == "--module"):
		if j+1 == len(words) {
			return nil, ReasonPackageManagerStart
		}
		edit.end, edit.text = words[j+1].start, "python -m "
		return edit, ""
	case strings.HasPrefix(target, "-"):
		return nil, ReasonPackageManagerStart
	case tool == "uv" && strings.HasSuffix(target, ".py"):
		if layer != nil && layer.InlineScripts[path.Base(target)] {
			return nil, ReasonInlineScriptMetadata
		}
		edit.text = "python "
		return edit, ""
	case tool == "pipenv" && layer != nil && layer.DotEnv:
		return nil, ReasonPipenvDotEnv
	case !pythonRunTarget(target, layer):
		return nil, ReasonRunTargetUnresolved
	}
	return edit, ""
}

// pythonRunTarget reports whether target, the program `<tool> run` would
// start, is what the shell finds with the virtualenv first on PATH: its
// Python, one of its scripts, or a path.
func pythonRunTarget(target string, layer *PythonLayer) bool {
	if layer == nil || strings.Contains(target, "/") || venvInterpreterName(target) {
		return true
	}
	return layer.Scripts[target]
}

// mentionsPythonPackageManager reports whether any word-like part of command
// names a Python package manager, for syntax splitShellCommands refuses.
func mentionsPythonPackageManager(command string) bool {
	fields := strings.FieldsFunc(command, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_./", r)
	})
	return slices.ContainsFunc(fields, PythonPackageManager)
}

// shellAssignment reports whether word is a NAME=value prefix of a command.
func shellAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// shellWord is one word of a start command: its text without quotes, and
// where it sits in the command.
type shellWord struct {
	text       string
	start, end int
}

// shellEdit replaces command[start:end] with text.
type shellEdit struct {
	start, end int
	text       string
}

// splitShellCommands splits a start command into the words of its simple
// commands, which ; & && || | and newlines separate. ok is false for syntax
// it does not follow: subshells and groups, command substitution,
// here-documents, unterminated quotes.
func splitShellCommands(command string) ([][]shellWord, bool) {
	var (
		commands [][]shellWord
		words    []shellWord
		text     strings.Builder
		start    = -1
		ok       = true
	)
	endWord := func(end int) {
		if start < 0 {
			return
		}
		if raw := command[start:end]; raw == "{" || raw == "}" {
			ok = false
		}
		words = append(words, shellWord{text: text.String(), start: start, end: end})
		text.Reset()
		start = -1
	}
	endCommand := func(end int) {
		endWord(end)
		if len(words) > 0 {
			commands = append(commands, words)
			words = nil
		}
	}
	begin := func(i int) {
		if start < 0 {
			start = i
		}
	}
	for i := 0; i < len(command) && ok; i++ {
		c := command[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			endWord(i)
		case c == '\n' || c == ';':
			endCommand(i)
		case c == '&' && (start >= 0 && (command[i-1] == '>' || command[i-1] == '<') || strings.HasPrefix(command[i+1:], ">")):
			// A redirection: 2>&1, &>file.
			begin(i)
			text.WriteByte(c)
		case c == '&' || c == '|':
			endCommand(i)
			if i+1 < len(command) && command[i+1] == c {
				i++
			}
		case c == '(' || c == ')' || c == '`' || strings.HasPrefix(command[i:], "$(") || strings.HasPrefix(command[i:], "<<"):
			ok = false
		case c == '#' && start < 0:
			// A comment runs to the end of the line.
			for i+1 < len(command) && command[i+1] != '\n' {
				i++
			}
		case c == '\\':
			begin(i)
			if i+1 < len(command) {
				i++
				if command[i] != '\n' {
					text.WriteByte(command[i])
				}
			}
		case c == '\'':
			begin(i)
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				ok = false
				break
			}
			text.WriteString(command[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			begin(i)
			quoted, end, closed := doubleQuoted(command, i)
			if !closed {
				ok = false
				break
			}
			text.WriteString(quoted)
			i = end
		default:
			begin(i)
			text.WriteByte(c)
		}
	}
	if !ok {
		return nil, false
	}
	endCommand(len(command))
	return commands, ok
}

// doubleQuoted reads the double-quoted string that opens at command[i]: its
// text and the index of the closing quote. closed is false when it does not
// close or holds a command substitution.
func doubleQuoted(command string, i int) (string, int, bool) {
	var text strings.Builder
	for i++; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '"':
			return text.String(), i, true
		case c == '`' || strings.HasPrefix(command[i:], "$("):
			return "", 0, false
		case c == '\\' && i+1 < len(command) && strings.IndexByte("$`\"\\\n", command[i+1]) >= 0:
			i++
			if command[i] != '\n' {
				text.WriteByte(command[i])
			}
		default:
			text.WriteByte(c)
		}
	}
	return "", 0, false
}
