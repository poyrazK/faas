package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Completed words are parsed as data, consuming option values before looking
// for --view. Missing/invalid views leave the shell's normal suggestions intact.
func completeLogViewFlags(words []string, contextOnly bool) (int, bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) < 3 || words[0] != "logs" || words[1] == "views" || words[1] == "tail" {
		return 1, false
	}
	prefix := words[len(words)-1]
	if prefix != "" && (!strings.HasPrefix(prefix, "--") || strings.Contains(prefix, "=")) {
		return 1, false
	}
	command, ok := lookupCliCommand("logs")
	if !ok {
		return 1, false
	}
	flags := map[string]cliFlag{}
	for _, option := range command.Flags {
		flags["--"+option.Name] = option
	}
	viewName := ""
	savedView := false
	provided := map[string]bool{}
	clearEnabled := map[string]bool{}
	positionals := 0
	for i := 1; i < len(words)-1; i++ {
		word := words[i]
		if word == "--" {
			return 1, false
		}
		if !strings.HasPrefix(word, "-") {
			positionals++
			if positionals > 1 {
				return 1, false
			}
			continue
		}
		name, value, inline := strings.Cut(word, "=")
		option, known := flags[name]
		if !known {
			return 1, false
		}
		boolValue := true
		if cliFlagTakesValue(option) {
			if !inline {
				i++
				if i >= len(words)-1 {
					return 1, false
				}
				value = words[i]
			}
		} else if inline {
			var err error
			boolValue, err = strconv.ParseBool(value)
			if err != nil {
				return 1, false
			}
		}
		provided[name] = true
		if strings.HasPrefix(name, "--clear-") {
			clearEnabled[name] = boolValue
		}
		if name == "--view" {
			viewName = value
		}
		if name == "--saved-view" {
			savedView = boolValue
		}
	}
	if savedView {
		if provided["--view"] {
			return 1, false
		}
		defaults, _, err := readLogDefaults()
		if err != nil {
			return 1, false
		}
		viewName = defaults.View
	}
	if viewName == "" {
		return 1, false
	}
	views, err := loadLogViews()
	if err != nil {
		return 1, false
	}
	view, found := views.Views[viewName]
	if !found {
		return 1, false
	}
	if contextOnly {
		return 0, true
	}
	selector := "--view"
	if savedView {
		selector = "--saved-view"
	}
	names := []string{"--app", selector, "--dry-run", "--since"}
	switch {
	case view.LatestRequestError:
		names = append(names, "--route", "--clear-route")
	case view.Source == logsSourceHTTP:
		names = append(names, "--route", "--status", "--limit", "--clear-route", "--clear-status")
	default:
		names = append(names, "--grep", "--level", "--follow", "--clear-grep", "--clear-level")
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, "--clear-") {
			if provided["--"+strings.TrimPrefix(name, "--clear-")] {
				continue
			}
		} else if clearEnabled["--clear-"+strings.TrimPrefix(name, "--")] {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			_, _ = fmt.Fprintln(osStdout, name)
		}
	}
	return 0, true
}
