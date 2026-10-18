package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Completion words are data. Only supported existing-name positions are read;
// new names and rename destinations are intentionally not completed.
func localNameCompletionTarget(words []string) (kind, prefix, inline string, ok bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) == 0 {
		return
	}
	prefix = words[len(words)-1]
	prior := words[:len(words)-1]
	if len(prior) == 0 {
		return
	}
	command := prior[0]
	subcommand, action := "", ""
	if len(prior) > 1 {
		subcommand = prior[1]
	}
	if len(prior) > 2 {
		action = prior[2]
	}
	if len(words) >= 4 && command == "jobs" && subcommand == "defaults" && action == "update" {
		for _, option := range []string{"--remove-include", "--remove-exclude"} {
			if strings.HasPrefix(prefix, option+"=") {
				return "job-" + strings.TrimPrefix(option, "--"), strings.TrimPrefix(prefix, option+"="), option + "=", true
			}
			if len(prior) >= 4 && prior[len(prior)-1] == option {
				return "job-" + strings.TrimPrefix(option, "--"), prefix, "", true
			}
		}
	}
	if len(words) >= 4 && command == "secrets" && subcommand == "requirements" && action == "remove" && !strings.HasPrefix(prefix, "-") {
		return "required-secret-names", prefix, "", true
	}
	if len(words) >= 3 && (command == "jobs" && subcommand == "resume" || command == "workflows" && subcommand == "resume-wait") {
		kind = "job"
		if command == "workflows" {
			kind = "workflow"
		}
		nameOption := "--" + kind
		options := []string{"--run", "--forget", nameOption}
		if kind == "workflow" {
			options = append(options, "--app")
		}
		for _, option := range options {
			targetKind := kind
			switch option {
			case nameOption:
				targetKind += "-names"
			case "--app":
				targetKind += "-apps"
			}
			if strings.HasPrefix(prefix, option+"=") {
				return targetKind, strings.TrimPrefix(prefix, option+"="), option + "=", true
			}
			if len(prior) >= 3 && prior[len(prior)-1] == option {
				return targetKind, prefix, "", true
			}
		}
		return "", "", "", false
	}
	if command == "context" && len(words) >= 3 {
		if len(prior) == 4 && subcommand == "presets" && action == "delete" && (prior[3] == "--dry-run" || prior[3] == "--dry-run=true" || prior[3] == "--dry-run=false") && !strings.HasPrefix(prefix, "-") {
			return "presets", prefix, "", true
		}
		if subcommand == "use" && (len(prior) == 2 || len(prior) == 3 && (prior[2] == "--dry-run" || prior[2] == "--dry-run=true" || prior[2] == "--dry-run=false")) && !strings.HasPrefix(prefix, "-") {
			return "presets", prefix, "", true
		}
		if len(prior) == 3 && subcommand == "presets" && (action == "show" || action == "update" || action == "delete" || action == "rename" || action == "copy") && !strings.HasPrefix(prefix, "-") {
			return "presets", prefix, "", true
		}
	}
	if command == "logs" {
		if len(prior) >= 3 && subcommand == "defaults" && action == "set" && !strings.HasPrefix(prefix, "-") && logDefaultsSetNamePosition(prior[3:]) {
			return "views", prefix, "", true
		}
		if len(prior) >= 3 && subcommand == "views" && action == "show" && !strings.HasPrefix(prefix, "-") && logViewShowNamePosition(prior[3:]) {
			return "views", prefix, "", true
		}
		if len(prior) == 4 && subcommand == "views" && action == "delete" && (prior[3] == "--dry-run" || prior[3] == "--dry-run=true" || prior[3] == "--dry-run=false") && !strings.HasPrefix(prefix, "-") {
			return "views", prefix, "", true
		}
		if len(prior) == 3 && subcommand == "views" && (action == "update" || action == "delete" || action == "rename" || action == "copy") && !strings.HasPrefix(prefix, "-") {
			return "views", prefix, "", true
		}
		if subcommand != "views" {
			if strings.HasPrefix(prefix, "--view=") {
				return "views", strings.TrimPrefix(prefix, "--view="), "--view=", true
			}
			if prior[len(prior)-1] == "--view" {
				return "views", prefix, "", true
			}
		}
	}
	return
}

// Only complete the first positional name. Option values are consumed as data,
// and an unfinished --app value belongs to app completion, not saved names.
func logViewShowNamePosition(prior []string) bool {
	for i := 0; i < len(prior); i++ {
		option, value, inline := strings.Cut(prior[i], "=")
		switch option {
		case "--details":
			if inline {
				if _, err := strconv.ParseBool(value); err != nil {
					return false
				}
			}
		case "--app":
			if !inline {
				i++
				if i >= len(prior) {
					return false
				}
			}
		case "--":
			return i == len(prior)-1
		default:
			return false
		}
	}
	return true
}

func cmdCompletionLocalNames(words []string, contextOnly bool) int {
	if code, handled := completeJobRecoveryTargets(words, contextOnly); handled {
		return code
	}
	if code, handled := completeWorkflowDefaults(words, contextOnly); handled {
		return code
	}
	if code, handled := completeLogViewTargetApps(words, contextOnly); handled {
		return code
	}
	if code, handled := completeLogViewFlags(words, contextOnly); handled {
		return code
	}
	kind, prefix, inline, ok := localNameCompletionTarget(words)
	if !ok {
		return 1
	}
	if contextOnly {
		return 0
	}
	names := []string{}
	switch kind {
	case "job-remove-include", "job-remove-exclude":
		names = completionJobDefaultPatterns(words, "--"+strings.TrimPrefix(kind, "job-"))
	case "required-secret-names":
		path, err := discoverRequiredSecretFile()
		if err != nil || path == "" {
			return 0
		}
		keys, err := readRequiredSecretKeys(path)
		if err != nil {
			return 0
		}
		if len(words) > 0 && words[0] == "--" {
			words = words[1:]
		}
		seen := map[string]bool{}
		for _, key := range words[3 : len(words)-1] {
			seen[key] = true
		}
		for _, key := range keys {
			if !seen[key] {
				names = append(names, key)
				seen[key] = true
			}
		}
	case "presets":
		data, _, err := loadContextPresets()
		if err != nil {
			return 0
		}
		for name := range data.Presets {
			names = append(names, name)
		}
	case "views":
		data, err := loadLogViews()
		if err != nil {
			return 0
		}
		for name := range data.Views {
			names = append(names, name)
		}
	default:
		workloadNames := strings.HasSuffix(kind, "-names")
		workloadApps := strings.HasSuffix(kind, "-apps")
		runKind := strings.TrimSuffix(strings.TrimSuffix(kind, "-names"), "-apps")
		selectedName, nameSet, valid := "", false, true
		selectedApp, appSet, savedWorkflow := "", false, false
		if runKind == "workflow" {
			savedWorkflow, valid = recordedCompletionSavedWorkflow(words)
			if !valid {
				return 0
			}
		}
		if savedWorkflow {
			defaults, _, err := readWorkflowDefaults()
			if err != nil {
				return 0
			}
			selectedName, nameSet, selectedApp, appSet = defaults.Workflow, true, defaults.App, true
		} else {
			if !workloadNames {
				selectedName, nameSet, valid = recordedCompletionNameFilter(words, runKind)
			}
			if !valid || nameSet && selectedName == "" {
				return 0
			}
			selectedApp, appSet, valid = recordedCompletionAppFilter(words, runKind)
		}
		if !valid {
			return 0
		}
		records, err := loadAcceptedRunRecords(runKind)
		if err != nil {
			return 0
		}
		seen := map[string]bool{}
		for _, record := range records {
			if record.Profile != currentProfile() || record.API != apiBase() || nameSet && record.Name != selectedName || appSet && record.App != selectedApp {
				continue
			}
			candidate := record.RunID
			if workloadNames {
				candidate = record.Name
			} else if workloadApps {
				candidate = record.App
				if candidate == "" {
					continue
				}
			}
			if !seen[candidate] && !strings.ContainsAny(candidate, "\r\n\t") {
				names = append(names, candidate)
				seen[candidate] = true
			}
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			_, _ = fmt.Fprintln(osStdout, inline+name)
		}
	}
	return 0
}

// Read completed options only, consuming values so a flag-looking value cannot
// accidentally become a workload selector. Repeated selectors use the last value,
// as the recovery command's flag parser does.
func recordedCompletionNameFilter(words []string, kind string) (name string, selected, valid bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	prior := words[2 : len(words)-1]
	savedJob, forgetting := false, strings.HasPrefix(words[len(words)-1], "--forget=")
	latest := false
	savedWorkflow := false
	// The final completed word is the option whose value is being completed.
	if len(prior) > 0 && (prior[len(prior)-1] == "--run" || prior[len(prior)-1] == "--forget" || prior[len(prior)-1] == "--"+kind || kind == "workflow" && prior[len(prior)-1] == "--app") {
		forgetting = prior[len(prior)-1] == "--forget"
		prior = prior[:len(prior)-1]
	}
	for i := 0; i < len(prior); i++ {
		option, value, inline := strings.Cut(prior[i], "=")
		switch option {
		case "--app":
			if kind != "workflow" {
				return "", false, false
			}
			if !inline {
				i++
				if i >= len(prior) {
					return "", false, false
				}
			}
		case "--" + kind, "--run", "--forget", "--timeout", "--poll-interval":
			if option == "--forget" {
				forgetting = true
			}
			if !inline {
				i++
				if i >= len(prior) {
					return "", false, false
				}
				value = prior[i]
			}
			if option == "--"+kind {
				name, selected = value, true
			}
		case "--latest":
			latest = true
			if inline {
				var err error
				latest, err = strconv.ParseBool(value)
				if err != nil {
					return "", false, false
				}
			}
		case "--saved-job":
			if kind != "job" {
				return "", false, false
			}
			savedJob = true
			if inline {
				var err error
				savedJob, err = strconv.ParseBool(value)
				if err != nil {
					return "", false, false
				}
			}
		case "--saved-workflow":
			if kind != "workflow" {
				return "", false, false
			}
			savedWorkflow = true
			if inline {
				var err error
				savedWorkflow, err = strconv.ParseBool(value)
				if err != nil {
					return "", false, false
				}
			}
		case "--dry-run":
			if inline {
				if _, err := strconv.ParseBool(value); err != nil {
					return "", false, false
				}
			}
		case "--list", "--overview", "--logs", "--results", "--diagnose", "--current-connection":
		default:
			return "", false, false
		}
	}
	if savedWorkflow {
		return "", false, false
	}
	if latest {
		current := words[len(words)-1]
		appTarget := strings.HasPrefix(current, "--app=") || len(words) > 2 && words[len(words)-2] == "--app"
		if kind != "workflow" || !appTarget {
			return "", false, false
		}
	}
	if savedJob {
		if selected || forgetting {
			return "", false, false
		}
		defaults, _, err := readJobDefaults()
		if err != nil {
			return "", false, false
		}
		name, selected = defaults.Job, true
	}
	return name, selected, true
}

func logDefaultsSetNamePosition(prior []string) bool {
	for _, word := range prior {
		if word == "--dry-run" {
			continue
		}
		if strings.HasPrefix(word, "--dry-run=") {
			if _, err := strconv.ParseBool(strings.TrimPrefix(word, "--dry-run=")); err == nil {
				continue
			}
		}
		return false
	}
	return true
}
