package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdSecretRequirements(args []string) int {
	if len(args) > 0 && args[0] == "check" {
		return cmdSecretsRequiredConsistency(args[1:])
	}
	if len(args) == 0 || args[0] != "set" && args[0] != "add" && args[0] != "remove" && args[0] != "show" && args[0] != "clear" {
		return printErr("Invalid requirements command", errors.New("use secrets requirements set/add/remove KEY..., check --from-env-file PATH, show, or clear"))
	}
	keys := []string{}
	operation := args[0]
	dryRun := false
	sourceFile, sourceSet := "", false
	if args[0] == "set" || args[0] == "add" || args[0] == "remove" {
		if len(args) < 2 {
			return printErr("Missing required names", errors.New("provide at least one key name; use clear to remove the declaration"))
		}
		seen := map[string]bool{}
		expectingSource := false
		for _, key := range args[1:] {
			if expectingSource {
				if strings.HasPrefix(key, "--") {
					return printErr("Missing dotenv source", errors.New("--from-env-file requires a file path"))
				}
				sourceFile, expectingSource = key, false
				continue
			}
			if key == "--from-env-file" || strings.HasPrefix(key, "--from-env-file=") {
				if operation != "set" && operation != "add" || sourceSet {
					return printErr("Invalid dotenv source", errors.New("--from-env-file is supported once, on requirements set or add"))
				}
				sourceSet = true
				if key == "--from-env-file" {
					expectingSource = true
				} else {
					sourceFile = strings.TrimPrefix(key, "--from-env-file=")
				}
				continue
			}
			if key == "--dry-run" {
				dryRun = true
				continue
			}
			if value, ok := strings.CutPrefix(key, "--dry-run="); ok {
				var err error
				dryRun, err = strconv.ParseBool(value)
				if err != nil {
					return printErr("Invalid preview flag", errors.New("--dry-run requires a boolean value"))
				}
				continue
			}
			if api.ValidateSecretKey(key) != nil {
				return printErr("Invalid required name", errors.New("provide key names only, without values or flags"))
			}
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
		if expectingSource {
			return printErr("Missing dotenv source", errors.New("--from-env-file requires a file path"))
		}
		if sourceSet {
			if len(keys) > 0 || strings.TrimSpace(sourceFile) == "" {
				return printErr("Invalid dotenv source", errors.New("use a nonempty --from-env-file path without positional key names"))
			}
			var err error
			sourceFile, err = filepath.Abs(sourceFile)
			if err != nil {
				return printErr("Could not locate dotenv source", err)
			}
			keys, err = readRequiredSecretEnvNames(sourceFile)
			if err != nil {
				return printErr("Could not read dotenv names", err)
			}
		}
		if len(keys) == 0 {
			return printErr("Missing required names", errors.New("provide at least one key name; use clear to remove the declaration"))
		}
		data, err := json.Marshal(keys)
		if err != nil || len(data) > 65536 {
			return printErr("Requirements too large", errors.New("the JSON declaration must fit within 64 KiB"))
		}
	} else if len(args) != 1 {
		return printErr("Invalid requirements arguments", errors.New("show and clear accept no arguments"))
	}
	defaultsPath, err := jobDefaultsPath()
	if err != nil {
		return printErr("Could not locate requirements", err)
	}
	path := filepath.Join(filepath.Dir(defaultsPath), "required-secrets.json")
	if dryRun {
		plan, err := planRequiredSecretNames(path, args[0], keys)
		if err != nil {
			return printErr("Could not preview requirements", err)
		}
		plan.SourceFile = sourceFile
		return renderRequiredSecretPreview(plan)
	}
	declared, cleared, changed := false, false, false
	switch args[0] {
	case "set":
		if err := writeLocalSecretConfig(path, keys); err != nil {
			return printErr("Could not save requirements", err)
		}
		declared = true
		changed = true
	case "add", "remove":
		keys, changed, err = editRequiredSecretNames(path, args[0], keys)
		if err != nil {
			return printErr("Could not edit requirements", err)
		}
		declared = true
	case "show":
		found, err := discoverRequiredSecretFile()
		if err != nil {
			return printErr("Could not inspect requirements", err)
		}
		if found != "" {
			keys, err = readRequiredSecretKeys(found)
			if err != nil {
				return printErr("Could not read requirements", err)
			}
			declared = true
		}
	case "clear":
		parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
		if err != nil {
			return printErr("Could not inspect requirements directory", err)
		}
		defer parent.Close()
		if parent.root != nil {
			name := filepath.Base(path)
			info, err := parent.root.Lstat(name)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return printErr("Could not inspect requirements", err)
			}
			if err == nil {
				if !info.Mode().IsRegular() {
					return printErr("Invalid requirements file", errors.New("clear requires a regular file, not a symlink or directory"))
				}
				if err := parent.root.Remove(name); err != nil {
					return printErr("Could not clear requirements", err)
				}
				cleared = true
				changed = true
			}
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Keys       []string `json:"keys"`
			Path       string   `json:"path"`
			Declared   bool     `json:"declared"`
			Cleared    bool     `json:"cleared"`
			Changed    bool     `json:"changed"`
			SourceFile string   `json:"source_file,omitempty"`
		}{keys, path, declared, cleared, changed, sourceFile}))
	}
	if args[0] == "clear" {
		_, _ = fmt.Fprintf(osStdout, "Required-name declaration cleared. Completeness is now unknown unless requirements are supplied explicitly.\nPath: %s\n", oneLine(path))
	} else if !declared {
		_, _ = fmt.Fprintf(osStdout, "Required names are not declared; completeness is unknown.\nPath: %s\n", oneLine(path))
	} else {
		names := strings.Join(keys, ", ")
		if len(keys) == 0 {
			names = "(none; explicitly empty declaration)"
		}
		_, _ = fmt.Fprintf(osStdout, "Required names: %s\nPath: %s\n", names, oneLine(path))
		if sourceSet {
			_, _ = fmt.Fprintf(osStdout, "Names imported from: %s\n", oneLine(sourceFile))
		}
		_, _ = fmt.Fprintln(osStdout, "Used automatically by secrets check and env push --dry-run. No secret values were read or changed.")
	}
	return 0
}

func editRequiredSecretNames(path, operation string, requested []string) ([]string, bool, error) {
	plan, err := planRequiredSecretNames(path, operation, requested)
	if err != nil {
		return nil, false, err
	}
	if plan.WouldChange {
		if err := writeLocalSecretConfig(path, plan.After); err != nil {
			return nil, false, err
		}
	}
	return plan.After, plan.WouldChange, nil
}

func planRequiredSecretNames(path, operation string, requested []string) (requiredSecretPreview, error) {
	plan := requiredSecretPreview{Path: path, Operation: operation, DryRun: true, Before: []string{}, After: []string{}, Added: []string{}, Removed: []string{}}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return plan, err
	}
	defer parent.Close()
	found, err := discoverRequiredSecretFile()
	if err != nil {
		return plan, err
	}
	before := []string{}
	if found != "" {
		if found != path {
			return plan, errors.New("requirements root changed; retry from the intended checkout")
		}
		before, err = readRequiredSecretKeys(found)
		if err != nil {
			return plan, err
		}
	}
	selected := map[string]bool{}
	for _, key := range requested {
		selected[key] = true
	}
	after := slices.Clone(before)
	switch operation {
	case "set":
		after = slices.Clone(requested)
	case "add":
		existing := map[string]bool{}
		for _, key := range before {
			existing[key] = true
		}
		for _, key := range requested {
			if !existing[key] {
				after = append(after, key)
				existing[key] = true
			}
		}
	default:
		existing := map[string]bool{}
		for _, key := range before {
			existing[key] = true
		}
		for _, key := range requested {
			if !existing[key] {
				return plan, fmt.Errorf("required name %s is not declared; no names were removed", key)
			}
		}
		after = []string{}
		for _, key := range before {
			if !selected[key] {
				after = append(after, key)
			}
		}
	}
	plan.Before, plan.After, plan.BeforeDeclared = before, after, found != ""
	plan.WouldChange = found == "" || !slices.Equal(before, after)
	plan.Added = requiredSecretNameDifference(after, before)
	plan.Removed = requiredSecretNameDifference(before, after)

	data, err := json.Marshal(after)
	if err != nil || len(data) > 65536 {
		return plan, errors.New("the JSON declaration must fit within 64 KiB")
	}
	current, err := discoverRequiredSecretFile()
	if err != nil || current != found {
		return plan, errors.New("requirements declaration changed; retry the edit")
	}
	if current != "" {
		keys, err := readRequiredSecretKeys(current)
		if err != nil || !slices.Equal(keys, before) {
			return plan, errors.New("requirements declaration changed; retry the edit")
		}
	}
	return plan, nil
}
