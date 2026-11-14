package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

// configExplainUsage lists every key `gregale config explain` accepts
// (ADR-20261107180000).
const configExplainUsage = "usage: gregale config explain <project|app|environment|service_policy|services.<name>.<service_policy|visibility|depends_on>|api-base|json>"

// cmdConfigExplain shows a setting's effective value and every layer that
// can set it, so a user can tell which file to edit. It only reads local
// files.
func cmdConfigExplain(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, configExplainUsage, "config")
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read the working directory", err)
	}
	explanation, err := explainConfigKey(strings.TrimSpace(args[0]), cwd)
	if err != nil {
		return printErr("Could not explain setting", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(explanation))
	}
	renderSettingExplanation(osStdout, explanation)
	return 0
}

func explainConfigKey(key, cwd string) (reposcan.SettingExplanation, error) {
	switch key {
	case "project", "app", "environment":
		return explainTargetKey(key, cwd)
	case "service_policy":
	default:
		if !strings.HasPrefix(key, "services.") {
			return explainCLIKey(key)
		}
	}
	root, err := projectContextRoot(cwd)
	if err != nil {
		return reposcan.SettingExplanation{}, err
	}
	return reposcan.ExplainProjectSetting(os.DirFS(root), key)
}

// explainTargetKey mirrors targetProjectContext: a flag wins, then the
// nearest .gregale/project.json as a whole, then the committed gregale.yaml
// `project:` block (ADR-20261107180000 slice 3). The environment is per
// developer and never comes from a manifest.
func explainTargetKey(key, cwd string) (reposcan.SettingExplanation, error) {
	linked, linkedPath, err := findProjectContext(cwd)
	linkedFound := err == nil
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		return reposcan.SettingExplanation{}, err
	}
	flag := map[string]string{"project": "--project", "app": "--app", "environment": "--environment"}[key]
	pick := func(c localProjectContext) string {
		switch key {
		case "project":
			return c.Project
		case "app":
			return c.App
		}
		return c.Environment
	}
	out := reposcan.SettingExplanation{Key: key}
	out.Layers = append(out.Layers, reposcan.SettingLayer{Source: flag + " flag", Note: "wins when passed to a command"})
	linkedLayer := reposcan.SettingLayer{Source: ".gregale/project.json", Note: "not linked; `gregale link` writes it"}
	if linkedFound {
		linkedLayer = reposcan.SettingLayer{Source: displayProjectContextPath(cwd, linkedPath), Value: pick(linked), Set: pick(linked) != "", Wins: pick(linked) != ""}
		out.Value = pick(linked)
	}
	out.Layers = append(out.Layers, linkedLayer)
	if key != "environment" {
		committed, committedPath, err := committedProjectContext(cwd)
		if err != nil && !errors.Is(err, errProjectContextNotFound) {
			return reposcan.SettingExplanation{}, err
		}
		layer := reposcan.SettingLayer{Source: "gregale.yaml `project:`", Field: "project." + map[string]string{"project": "name", "app": "app"}[key]}
		if err == nil {
			layer.Source = displayProjectContextPath(cwd, committedPath) + " `project:`"
			layer.Value = pick(committed)
			layer.Set = layer.Value != ""
		}
		switch {
		case linkedFound && layer.Set:
			layer.Note = "ignored while .gregale/project.json exists"
		case !linkedFound && layer.Set:
			layer.Wins = true
			out.Value = layer.Value
		}
		out.Layers = append(out.Layers, layer)
	}
	if out.Value == "" {
		out.Layers = append(out.Layers, reposcan.SettingLayer{Source: "default", Set: true, Wins: true, Note: "none; app-scoped commands need a flag"})
		if key == "environment" {
			out.Layers[len(out.Layers)-1].Note = "the command's default environment"
		}
	}
	return out, nil
}

// explainCLIKey covers the local CLI settings `gregale config get` reads.
func explainCLIKey(raw string) (reposcan.SettingExplanation, error) {
	key := normalizeConfigKey(raw)
	if key == "" {
		return reposcan.SettingExplanation{}, fmt.Errorf("unknown setting %q (%s)", raw, strings.TrimPrefix(configExplainUsage, "usage: gregale config explain "))
	}
	cfg, err := loadCLIConfig()
	if err != nil {
		return reposcan.SettingExplanation{}, err
	}
	entry, _ := configEntry(displayConfigEntries(cfg), key)
	return reposcan.SettingExplanation{Key: entry.Key, Value: entry.Value, Layers: []reposcan.SettingLayer{
		{Source: entry.Source, Value: entry.Value, Set: true, Wins: true},
	}}, nil
}

func renderSettingExplanation(w io.Writer, e reposcan.SettingExplanation) {
	value := e.Value
	if value == "" {
		value = "(not set)"
	}
	_, _ = fmt.Fprintf(w, "%s = %s\n", e.Key, value)
	for i, layer := range e.Layers {
		where := layer.Source
		if layer.Field != "" {
			where += " " + layer.Field
		}
		declared := "(not set)"
		if layer.Set {
			declared = layer.Value
		}
		if layer.Source == "default" && layer.Value == "" {
			declared = "-"
		}
		line := fmt.Sprintf("  %d. %-52s %s", i+1, where, declared)
		if layer.Wins {
			line += "  <- wins"
		}
		if layer.Note != "" {
			line += "  (" + layer.Note + ")"
		}
		_, _ = fmt.Fprintln(w, line)
	}
	for _, warning := range e.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", warning)
	}
}
