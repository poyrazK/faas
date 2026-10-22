package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Complete explicit Job/run positions only. Flag values and completion probes
// never resolve preferences or contact the API.
func completeJobRecoveryTargets(words []string, contextOnly bool) (int, bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) < 3 || words[0] != "jobs" {
		return 0, false
	}
	defaults := len(words) >= 4 && words[1] == "defaults" && (words[2] == "set" || words[2] == "update")
	start, commandName := 2, words[1]
	if defaults {
		start, commandName = 3, words[2]
	} else {
		switch commandName {
		case "retry", "replay-failed", "cancel", "status", "wait", "results", "tasks", "logs", "attempts":
		default:
			return 0, false
		}
	}
	prior, prefix := words[start:len(words)-1], words[len(words)-1]
	flags := map[string]cliFlag{}
	command, _ := lookupCliCommand("jobs")
	subcommands := command.Subcommands
	if defaults {
		for _, sub := range command.Subcommands {
			if sub.Name == "defaults" {
				subcommands = sub.Subcommands
				break
			}
		}
	}
	for _, sub := range subcommands {
		if sub.Name == commandName {
			for _, flag := range sub.Flags {
				flags[flag.Name] = flag
			}
		}
	}
	positionals := []string{}
	positionalOnly, taskSet, interactive := false, false, false
	for i := 0; i < len(prior); i++ {
		word := prior[i]
		if !positionalOnly && word == "--" {
			positionalOnly = true
			continue
		}
		if !positionalOnly && strings.HasPrefix(word, "-") {
			option, value, inline := strings.Cut(strings.TrimLeft(word, "-"), "=")
			meta, exists := flags[option]
			if !exists {
				return 0, true
			}
			if meta.Bool {
				enabled := true
				if inline {
					var err error
					enabled, err = strconv.ParseBool(value)
					if err != nil {
						return 0, true
					}
				}
				if option == "interactive" {
					if words[1] != "wait" {
						return 0, true
					}
					interactive = enabled
				}
				continue
			}
			if !inline {
				i++
				if i == len(prior) {
					return 0, false
				}
				value = prior[i]
			}
			switch option {
			case "task":
				index, err := strconv.Atoi(value)
				if taskSet || err != nil || index < 0 {
					return 0, true
				}
				taskSet = true
			case "tasks":
				count, err := strconv.Atoi(value)
				if err != nil || count <= 0 {
					return 0, true
				}
			}
			continue
		}
		positionals = append(positionals, word)
	}
	if interactive {
		return 0, true
	}
	if !positionalOnly && strings.HasPrefix(prefix, "-") {
		return 0, false
	}
	if defaults && len(positionals) > 0 {
		return 0, false
	}
	if len(positionals) > 1 {
		return 0, true
	}
	if len(positionals) == 1 && !jobSlugPattern.MatchString(positionals[0]) {
		return 0, true
	}
	if contextOnly {
		return 0, true
	}
	path, err := jobDefaultsPath()
	if err != nil {
		return 0, true
	}
	records, err := readRecordedReadinessRecords(filepath.Join(filepath.Dir(path), "accepted-runs"))
	if err != nil {
		return 0, true
	}
	profile, endpoint := currentProfile(), apiBase()
	names := []string{}
	seen := map[string]bool{}
	for _, record := range records {
		if record.Kind != "job" || record.Profile != profile || record.API != endpoint || !jobSlugPattern.MatchString(record.Name) || len(positionals) == 1 && record.Name != positionals[0] {
			continue
		}
		candidate := record.Name
		if len(positionals) == 1 {
			candidate = record.RunID
		}
		if !seen[candidate] && strings.HasPrefix(candidate, prefix) {
			seen[candidate] = true
			names = append(names, candidate)
		}
	}
	sort.Strings(names)
	for _, candidate := range names {
		_, _ = fmt.Fprintln(osStdout, candidate)
	}
	return 0, true
}
