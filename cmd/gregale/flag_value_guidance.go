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
type flagValueAdvice struct {
	name, kind, expectedType, marker string
	allowed                          []string
	expected, example                string
}

func flagValueGuidance(diagnostic string, fs *flag.FlagSet, path string) flagValueAdvice {
	advice := flagValueAdvice{}
	if fs == nil {
		return advice
	}
	if rest, ok := strings.CutPrefix(diagnostic, "flag --"); ok {
		if name, ok := strings.CutSuffix(rest, " needs a value"); ok {
			advice.name, advice.kind = name, "missing_value"
		}
	} else if match := invalidFlagValueName.FindStringSubmatch(diagnostic); len(match) == 2 {
		advice.name, advice.kind = match[1], "invalid_value"
	}
	f := fs.Lookup(advice.name)
	if f == nil {
		return flagValueAdvice{}
	}
	documented, ok := documentedParserFlag(fs.Name(), advice.name)
	if !ok {
		documented, ok = documentedParserFlag(path, advice.name)
	}
	if ok {
		advice.marker = documented.Value
	}
	if ok && len(documented.ClosedSet) > 0 {
		advice.expectedType, advice.allowed = "enum", documented.ClosedSet
		return advice
	}
	if getter, ok := f.Value.(flag.Getter); ok {
		switch getter.Get().(type) {
		case time.Duration:
			advice.expectedType, advice.expected, advice.example = "duration", "a duration", "2s"
		case bool:
			advice.expectedType, advice.expected, advice.example = "boolean", "true or false", "true"
		case int, int64, uint, uint64:
			advice.expectedType, advice.expected, advice.example = "integer", "a whole number", "1"
		case float64:
			advice.expectedType, advice.expected, advice.example = "number", "a number", "1.5"
		}
	}
	if advice.expected == "" && ok && documented.Value != "" {
		advice.expectedType = "value"
		advice.expected = "a value in the documented format " + documented.Value
	}
	return advice
}

func (advice flagValueAdvice) humanHint() string {
	if len(advice.allowed) > 0 {
		return "Allowed values: " + strings.Join(advice.allowed, ", ")
	}
	if advice.expected == "" {
		return ""
	}
	guidance := "Expected: " + advice.expected + "."
	if advice.example != "" {
		guidance += fmt.Sprintf("\n  Format example: --%s %s (command-specific limits still apply).", advice.name, advice.example)
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
