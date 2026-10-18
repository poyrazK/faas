package main

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Help stays readable at 80 columns even when a flag's spelling or value set
// needs its own line. Keep this deterministic for pipes and generated previews.
func printHelpEntry(w io.Writer, label, description string) {
	const labelWidth = 30
	if utf8.RuneCountInString(label) > labelWidth {
		_, _ = fmt.Fprintf(w, "  %s\n", label)
		printHelpText(w, "      ", description)
		return
	}
	prefix := fmt.Sprintf("  %-*s ", labelWidth, label)
	printHelpWrapped(w, prefix, strings.Repeat(" ", labelWidth+3), description)
}

func printHelpText(w io.Writer, indent, text string) {
	printHelpWrapped(w, indent, indent, text)
}

func printHelpWrapped(w io.Writer, prefix, continuation, text string) {
	line := prefix
	for _, word := range strings.Fields(text) {
		separator := ""
		if line != prefix {
			separator = " "
		}
		if line != prefix && utf8.RuneCountInString(line+separator+word) > 80 {
			_, _ = fmt.Fprintln(w, line)
			prefix = continuation
			line, separator = prefix, ""
		}
		line += separator + word
	}
	_, _ = fmt.Fprintln(w, line)
}

func printHelpNotes(w io.Writer, notes []string) {
	if len(notes) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w)
	for _, note := range notes {
		printHelpText(w, "", note)
	}
}

// Groups follow their first appearance in the manifest; flags retain their
// order within each group. Completion and references still see every flag.
func printLocalHelpFlags(w io.Writer, flags []cliFlag, compact bool) {
	var groups []string
	byGroup := map[string][]cliFlag{}
	for _, flag := range flags {
		if compact && !flag.Common && !flag.Req {
			continue
		}
		group := flag.Group
		if compact {
			group = "Common flags"
		} else if group == "" {
			group = "Flags"
		}
		if _, ok := byGroup[group]; !ok {
			groups = append(groups, group)
		}
		byGroup[group] = append(byGroup[group], flag)
	}
	for _, group := range groups {
		_, _ = fmt.Fprintf(w, "\n%s:\n", group)
		for _, flag := range byGroup[group] {
			printLocalHelpFlag(w, flag)
		}
	}
}
