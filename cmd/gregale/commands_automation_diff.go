package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationDefinitionChange struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

type automationDefinitionDiff struct {
	Name             string                       `json:"name"`
	DraftVersion     int64                        `json:"draft_version"`
	PublishedVersion int64                        `json:"published_version"`
	FirstPublication bool                         `json:"first_publication"`
	LiveEnabled      bool                         `json:"live_enabled"`
	Changed          bool                         `json:"changed"`
	Changes          []automationDefinitionChange `json:"changes"`
}

func diffAutomationDefinition(automation api.AutomationResponse) (automationDefinitionDiff, error) {
	report := automationDefinitionDiff{Name: automation.Name, DraftVersion: automation.Version, PublishedVersion: automation.PublishedVersion, FirstPublication: automation.Published == nil, LiveEnabled: automation.Enabled, Changes: []automationDefinitionChange{}}
	// Compare fields as raw JSON to retain exact numbers and distinguish null from absence.
	normalize := func(spec api.WorkflowSpec) (map[string]json.RawMessage, error) {
		data, err := json.Marshal(spec)
		if err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		// Key steps by name so insertions do not label every subsequent step as changed.
		var steps []map[string]json.RawMessage
		if err = json.Unmarshal(fields["steps"], &steps); err != nil {
			return nil, err
		}
		keyed := map[string]json.RawMessage{}
		order := []string{}
		for _, step := range steps {
			var name string
			if err = json.Unmarshal(step["name"], &name); err != nil {
				return nil, err
			}
			if _, exists := keyed[name]; exists {
				return nil, errors.New("definition contains duplicate step names")
			}
			order = append(order, name)
			keyed[name], err = json.Marshal(step)
			if err != nil {
				return nil, err
			}
		}
		fields["steps"], err = json.Marshal(keyed)
		if err != nil {
			return nil, err
		}
		fields["step_order"], err = json.Marshal(order)
		return fields, err
	}
	draft, err := normalize(automation.Draft)
	if err != nil {
		return report, err
	}
	published := map[string]json.RawMessage{}
	if automation.Published != nil {
		published, err = normalize(*automation.Published)
		if err != nil {
			return report, err
		}
	}
	var compare func(string, map[string]json.RawMessage, map[string]json.RawMessage)
	compare = func(prefix string, before, after map[string]json.RawMessage) {
		keys := map[string]bool{}
		for key := range before {
			keys[key] = true
		}
		for key := range after {
			keys[key] = true
		}
		sorted := make([]string, 0, len(keys))
		for key := range keys {
			sorted = append(sorted, key)
		}
		sort.Strings(sorted)
		for _, key := range sorted {
			path := prefix + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			old, oldExists := before[key]
			next, nextExists := after[key]
			kind := "modified"
			if !oldExists {
				kind = "added"
			} else if !nextExists {
				kind = "removed"
			} else if equalAutomationScenarioJSON(old, next) {
				continue
			}
			var oldMap, nextMap map[string]json.RawMessage
			if oldExists && nextExists && json.Unmarshal(old, &oldMap) == nil && json.Unmarshal(next, &nextMap) == nil && oldMap != nil && nextMap != nil {
				compare(path, oldMap, nextMap)
			} else {
				report.Changes = append(report.Changes, automationDefinitionChange{Path: path, Change: kind})
			}
		}
	}
	compare("", published, draft)
	report.Changed = len(report.Changes) != 0
	return report, nil
}

func printAutomationDefinitionDiff(report automationDefinitionDiff) int {
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	if _, err := fmt.Fprintf(osStdout, "Automation %q: published version %d → draft version %d; live automatic starts enabled: %t\n", report.Name, report.PublishedVersion, report.DraftVersion, report.LiveEnabled); err != nil {
		return printErr("Could not write automation diff", err)
	}
	if report.FirstPublication {
		if _, err := fmt.Fprintln(osStdout, "First publication (no published definition)."); err != nil {
			return printErr("Could not write automation diff", err)
		}
	}
	if !report.Changed {
		if _, err := fmt.Fprintln(osStdout, "No definition changes."); err != nil {
			return printErr("Could not write automation diff", err)
		}
	}
	for _, change := range report.Changes {
		if _, err := fmt.Fprintf(osStdout, "  %s %q\n", change.Change, change.Path); err != nil {
			return printErr("Could not write automation diff", err)
		}
	}
	return 0
}

func cmdAutomationsDiff(args []string) int {
	fs := newFlagSet("automations diff", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validAutomationRunSelection(*app, *name) {
		return printErr("Invalid automation selection", errors.New("provide --app and a valid --name"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	automation, err := client.GetAutomation(ctx, *app, *name)
	if err != nil {
		return printErr("Could not load automation", err)
	}
	if automation.Name != *name || automation.Draft.Name != *name {
		return printErr("Invalid automation response", errors.New("automation name does not match the selection"))
	}
	report, err := diffAutomationDefinition(automation)
	if err != nil {
		return printErr("Could not compare automation definitions", err)
	}
	return printAutomationDefinitionDiff(report)
}
