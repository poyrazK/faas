package main

import (
	"flag"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var invalidFlagValueName = regexp.MustCompile(`^invalid (?:value .* for flag|boolean value .* for) --([A-Za-z0-9][A-Za-z0-9-]*):`)

// Guidance uses types and documentation, never defaults or supplied values.
func flagValueGuidance(diagnostic string, fs *flag.FlagSet, path string) string {
	if fs == nil {
		return ""
	}
	name := ""
	if rest, ok := strings.CutPrefix(diagnostic, "flag --"); ok {
		if value, ok := strings.CutSuffix(rest, " needs a value"); ok {
			name = value
		}
	} else if match := invalidFlagValueName.FindStringSubmatch(diagnostic); len(match) == 2 {
		name = match[1]
	}
	f := fs.Lookup(name)
	if f == nil {
		return ""
	}
	documented, ok := documentedParserFlag(fs.Name(), name)
	if !ok {
		documented, ok = documentedParserFlag(path, name)
	}
	if ok && len(documented.ClosedSet) > 0 {
		return "Allowed values: " + strings.Join(documented.ClosedSet, ", ")
	}
	expected, example := "", ""
	if getter, ok := f.Value.(flag.Getter); ok {
		switch getter.Get().(type) {
		case time.Duration:
			expected, example = "a duration", "2s"
		case bool:
			expected, example = "true or false", "true"
		case int, int64, uint, uint64:
			expected, example = "a whole number", "1"
		case float64:
			expected, example = "a number", "1.5"
		}
	}
	if expected == "" && ok && documented.Value != "" {
		expected = "a value in the documented format " + documented.Value
	}
	if expected == "" {
		return ""
	}
	guidance := "Expected: " + expected + "."
	if example != "" {
		guidance += fmt.Sprintf("\n  Format example: --%s %s (command-specific limits still apply).", name, example)
	}
	return guidance
}

func documentedParserFlag(path, name string) (cliFlag, bool) {
	words := strings.Fields(path)
	if len(words) == 0 {
		return cliFlag{}, false
	}
	command, ok := lookupCliCommand(words[0])
	if !ok {
		return cliFlag{}, false
	}
	flags, children := command.Flags, command.Subcommands
	for _, word := range words[1:] {
		child, ok := findCliSubcommand(children, word)
		if !ok {
			return cliFlag{}, false
		}
		flags, children = child.Flags, child.Subcommands
	}
	for _, f := range flags {
		if f.Name == name || (f.ShortName != "" && f.ShortName == name) {
			return f, true
		}
	}
	return cliFlag{}, false
}
