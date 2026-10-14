package main

import (
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Read only completed selectors; app candidates come from the same validated
// local records as recovery, never linked context or a remote app cache.
func recordedCompletionAppFilter(words []string, kind string) (app string, selected, valid bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	prior := words[2 : len(words)-1]
	current := words[len(words)-1]
	appTarget := strings.HasPrefix(current, "--app=") || len(prior) > 0 && prior[len(prior)-1] == "--app"
	forgetting := strings.HasPrefix(current, "--forget=") || len(prior) > 0 && prior[len(prior)-1] == "--forget"
	flags := map[string]cliFlag{}
	for _, option := range resumeRecordedRunCLIFlags(kind) {
		flags["--"+option.Name] = option
	}
	if len(prior) > 0 {
		if option, exists := flags[prior[len(prior)-1]]; exists && !option.Bool {
			prior = prior[:len(prior)-1]
		}
	}
	for i := 0; i < len(prior); i++ {
		option, value, inline := strings.Cut(prior[i], "=")
		meta, exists := flags[option]
		if !exists {
			return "", false, false
		}
		if meta.Bool {
			if inline {
				if _, err := strconv.ParseBool(value); err != nil {
					return "", false, false
				}
			}
			continue
		}
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
		if option == "--app" {
			app, selected = value, true
		}
	}
	if !appTarget && selected && !api.ValidAppSlug(app) || forgetting && (selected || appTarget) {
		return "", false, false
	}
	if appTarget {
		return "", false, true
	}
	return app, selected, true
}

// Resolve whether the opt-in saved selector is active before reading preferences.
// Conflicting selector completions stay empty and never trigger a file read.
func recordedCompletionSavedWorkflow(words []string) (active, valid bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) < 3 {
		return false, false
	}
	prior, current := words[2:len(words)-1], words[len(words)-1]
	conflict, runTarget, latest := false, strings.HasPrefix(current, "--run="), false
	for _, option := range []string{"--workflow", "--app", "--forget"} {
		if strings.HasPrefix(current, option+"=") || len(prior) > 0 && prior[len(prior)-1] == option {
			conflict = true
		}
	}
	flags := map[string]cliFlag{}
	for _, option := range resumeRecordedRunCLIFlags("workflow") {
		flags["--"+option.Name] = option
	}
	if len(prior) > 0 {
		last := prior[len(prior)-1]
		runTarget = runTarget || last == "--run"
		if option, exists := flags[last]; exists && !option.Bool {
			prior = prior[:len(prior)-1]
		}
	}
	for i := 0; i < len(prior); i++ {
		option, value, inline := strings.Cut(prior[i], "=")
		meta, exists := flags[option]
		if !exists {
			return false, false
		}
		if meta.Bool {
			enabled := true
			if inline {
				var err error
				enabled, err = strconv.ParseBool(value)
				if err != nil {
					return false, false
				}
			}
			switch option {
			case "--saved-workflow":
				active = enabled
			case "--latest":
				latest = enabled
			}
			continue
		}
		if option == "--workflow" || option == "--app" || option == "--forget" {
			conflict = true
		}
		if !inline {
			i++
			if i >= len(prior) {
				return false, false
			}
		}
	}
	if active && (conflict || latest && runTarget) {
		return false, false
	}
	return active, true
}
