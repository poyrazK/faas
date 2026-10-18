package main

import (
	"errors"
	"flag"
	"sort"
	"strings"
	"unicode"
)

// Suggestions are diagnostics only. The caller has already loaded and validated
// the local store; no lookup, target selection, or command execution occurs here.
func unknownSavedName[T any](title, query string, saved map[string]T, command func(string) []string, list []string) int {
	message := "Inspect saved names with: " + savedNameSuggestionCommand(list)
	if len(query) >= 2 && len(query) <= 64 {
		limit := 2
		if len(query) <= 3 {
			limit = 1
		}
		type match struct {
			name     string
			distance int
		}
		matches := []match{}
		for name := range saved {
			if strings.IndexFunc(name, unicode.IsControl) >= 0 {
				continue
			}
			if len(name)-len(query) > limit || len(query)-len(name) > limit {
				continue
			}
			if distance := levenshtein(query, name); distance <= limit {
				matches = append(matches, match{name, distance})
			}
		}
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].distance == matches[j].distance {
				return matches[i].name < matches[j].name
			}
			return matches[i].distance < matches[j].distance
		})
		if len(matches) > 3 {
			matches = matches[:3]
		}
		for _, candidate := range matches {
			message += "\nDid you mean " + candidate.name + "? Corrected command (POSIX shells):\n  " + savedNameSuggestionCommand(command(candidate.name))
		}
	}
	return printErr(title, errors.New(message))
}

func savedNameSuggestionCommand(args []string) string {
	words := []string{"gregale", "--profile", recoveryArgument(currentProfile())}
	for _, arg := range args {
		words = append(words, recoveryArgument(arg))
	}
	return strings.Join(words, " ")
}

// These callers use scalar flags; reconstruct only explicitly provided options
// and replace the selector, preserving dry-run and app targeting choices.
func correctedSavedNameFlags(prefix []string, fs *flag.FlagSet, name, selectorFlag string) []string {
	args := append([]string{}, prefix...)
	positional := append([]string{}, fs.Args()...)
	if selectorFlag == "" && len(positional) > 0 {
		positional[0] = name
	}
	args = append(args, positional...)
	fs.Visit(func(f *flag.Flag) {
		value := f.Value.String()
		if f.Name == selectorFlag {
			value = name
		}
		args = append(args, "--"+f.Name+"="+value)
	})
	return args
}
