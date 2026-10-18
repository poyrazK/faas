package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Parse completion words as data, without resolving preferences or records.
func completeWorkflowDefaults(words []string, contextOnly bool) (int, bool) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) < 4 || words[0] != "workflows" || words[1] != "defaults" || (words[2] != "set" && words[2] != "update") {
		return 0, false
	}
	prior, current := words[3:len(words)-1], words[len(words)-1]
	name, app, prefix, inline := "", "", current, ""
	nameSet, appSet, appTarget, positionalOnly := false, false, false, false
	for i := 0; i < len(prior); i++ {
		word := prior[i]
		if !positionalOnly {
			option, value, hasValue := strings.Cut(word, "=")
			switch option {
			case "--":
				positionalOnly = true
				continue
			case "--dry-run":
				if hasValue {
					if _, err := strconv.ParseBool(value); err != nil {
						return 0, true
					}
				}
				continue
			case "--app":
				if !hasValue {
					i++
					if i == len(prior) {
						appTarget = true
						continue
					}
					value = prior[i]
				}
				app, appSet = value, true
				continue
			}
			if strings.HasPrefix(word, "-") {
				return 0, true
			}
		}
		if nameSet {
			return 0, true
		}
		name, nameSet = word, true
	}
	if !positionalOnly && !appTarget && strings.HasPrefix(current, "--app=") {
		appTarget, prefix, inline = true, strings.TrimPrefix(current, "--app="), "--app="
	}
	if !appTarget && ((!positionalOnly && strings.HasPrefix(current, "-")) || nameSet) {
		return 0, false
	}
	if nameSet && strings.TrimSpace(name) == "" || !appTarget && appSet && !api.ValidAppSlug(app) {
		return 0, true
	}
	if contextOnly {
		return 0, true
	}
	path, err := workflowDefaultsPath()
	if err != nil {
		return 0, true
	}
	if words[2] == "update" {
		defaults, foundPath, err := readWorkflowDefaults()
		if err != nil || foundPath != path {
			return 0, true
		}
		if appTarget && !nameSet {
			name, nameSet = defaults.Workflow, true
		} else if !appTarget && !appSet {
			app, appSet = defaults.App, true
		}
	}
	records, err := readRecordedReadinessRecords(filepath.Join(filepath.Dir(path), "accepted-runs"))
	if err != nil {
		return 0, true
	}
	profile, endpoint := currentProfile(), apiBase()
	seen := map[string]bool{}
	names := []string{}
	for _, record := range records {
		if record.Kind != "workflow" || record.Profile != profile || record.API != endpoint || appTarget && nameSet && record.Name != name || !appTarget && appSet && record.App != app {
			continue
		}
		candidate := record.Name
		if appTarget {
			candidate = record.App
			if !api.ValidAppSlug(candidate) {
				continue
			}
		}
		if !seen[candidate] && !strings.ContainsAny(candidate, "\r\n\t") && (appTarget || positionalOnly || !strings.HasPrefix(candidate, "-")) && strings.HasPrefix(candidate, prefix) {
			seen[candidate] = true
			names = append(names, candidate)
		}
	}
	sort.Strings(names)
	for _, candidate := range names {
		_, _ = fmt.Fprintln(osStdout, inline+candidate)
	}
	return 0, true
}
