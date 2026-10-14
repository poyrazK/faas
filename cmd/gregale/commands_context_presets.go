package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type contextPresetsFile struct {
	Version int                            `json:"version"`
	Presets map[string]localProjectContext `json:"presets"`
}

func contextPresetsPath() (string, error) {
	path, err := jobDefaultsPath()
	if err != nil {
		return "", fmt.Errorf("locate project preset root: %w", err)
	}
	return filepath.Join(filepath.Dir(path), "context-presets.json"), nil
}

func loadContextPresets() (contextPresetsFile, string, error) {
	data := contextPresetsFile{Version: 1, Presets: map[string]localProjectContext{}}
	path, err := contextPresetsPath()
	if err != nil {
		return data, path, err
	}
	directory, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return data, path, fmt.Errorf("inspect presets directory: %w", err)
	}
	defer directory.Close()
	if directory.root == nil {
		return data, path, nil
	}
	file, err := openCustomerFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return data, path, nil
	}
	if err != nil {
		return data, path, fmt.Errorf("open context presets: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return data, path, fmt.Errorf("read context presets: %w", err)
	}
	if len(body) > 65536 {
		return data, path, errors.New("context presets exceed 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return data, path, errors.New("invalid context preset JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || data.Version != 1 || data.Presets == nil || len(data.Presets) > 100 {
		return data, path, errors.New("presets require one version-1 object with at most 100 named contexts")
	}
	for name, link := range data.Presets {
		if err := validateProfileName(name); err != nil {
			return data, path, errors.New("invalid context preset name")
		}
		if err := link.validate(); err != nil {
			return data, path, fmt.Errorf("invalid context preset: %w", err)
		}
	}
	return data, path, nil
}

func writeContextPresets(path string, data contextPresetsFile) error {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode context presets: %w", err)
	}
	if len(body) > 65536 || len(data.Presets) > 100 {
		return errors.New("presets exceed the 64 KiB or 100-name limit")
	}
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create preset directory: %w", err)
	}
	directory, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("inspect preset directory: %w", err)
	}
	defer directory.Close()
	if directory.root == nil {
		return errors.New("preset directory disappeared")
	}
	name := filepath.Base(path)
	if info, err := directory.root.Lstat(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect preset destination: %w", err)
	} else if err == nil && !info.Mode().IsRegular() {
		return errors.New("preset destination must be a regular file")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("prepare preset output: %w", err)
	}
	temporary := ".context-presets-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := directory.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create preset output: %w", err)
	}
	defer func() { _ = file.Close(); _ = directory.root.Remove(temporary) }()
	if _, err := file.Write(body); err != nil {
		return fmt.Errorf("write presets: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync presets: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close presets: %w", err)
	}
	if err := directory.root.Rename(temporary, name); err != nil {
		return fmt.Errorf("publish presets: %w", err)
	}
	return nil
}

func cmdContextPresets(args []string) int {
	if args[0] == "presets" {
		return manageContextPresets(args[1:])
	}
	fs := newFlagSet("context "+args[0], flag.ContinueOnError)
	replace, dryRun := new(bool), new(bool)
	if args[0] == "save" {
		fs.BoolVar(replace, "replace", false, "replace an existing named preset")
	} else {
		fs.BoolVar(dryRun, "dry-run", false, "validate and preview applying the preset")
	}
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		return printErr("Invalid preset arguments", errors.New("use context save NAME [--replace] or context use NAME [--dry-run]"))
	}
	name := fs.Arg(0)
	if err := validateProfileName(name); err != nil {
		return printErr("Invalid preset name", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not locate checkout", err)
	}
	link, linkPath, err := linkedProjectContext(cwd)
	if err != nil {
		return printErr("A linked project is required", err)
	}
	data, path, err := loadContextPresets()
	if err != nil {
		return printErr("Could not read context presets", err)
	}
	if args[0] == "save" {
		if _, exists := data.Presets[name]; exists && !*replace {
			return printErr("Preset already exists", errors.New("choose another name or explicitly use --replace"))
		}
		data.Presets[name] = link
		if err := writeContextPresets(path, data); err != nil {
			return printErr("Could not save context preset", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(struct {
				Name   string              `json:"name"`
				Preset localProjectContext `json:"preset"`
				Path   string              `json:"path"`
			}{name, link, path}))
		}
		_, _ = fmt.Fprintf(osStdout, "Saved context preset %s: project=%s; app=%s; environment=%s\nPath: %s\n", name, link.Project, contextDefaultLabel(link.App), contextDefaultLabel(link.Environment), oneLine(path))
		return 0
	}
	preset, exists := data.Presets[name]
	if !exists {
		return unknownSavedName("Unknown context preset", name, data.Presets, func(candidate string) []string {
			return correctedSavedNameFlags([]string{"context", args[0]}, fs, candidate, "")
		}, []string{"context", "presets", "list"})
	}
	if preset.Project != link.Project {
		return printErr("Preset belongs to another project", errors.New("link the intended project explicitly before using its preset; connection and project were not switched"))
	}
	update := []string{}
	if preset.App == "" {
		update = append(update, "--clear-app")
	} else {
		update = append(update, "--app", preset.App)
	}
	if preset.Environment == "" {
		update = append(update, "--clear-environment")
	} else {
		update = append(update, "--environment", preset.Environment)
	}
	if *dryRun {
		update = append(update, "--dry-run")
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Using context preset %s from %s\n", name, oneLine(path))
	}
	return cmdContextSetSelected(update, &contextUpdateSelection{Link: link, Path: linkPath, Preset: name, PresetPath: path})
}

func manageContextPresets(args []string) int {
	if len(args) > 0 && args[0] == "delete" {
		return cmdContextPresetDelete(args[1:])
	}
	if len(args) > 0 && args[0] == "copy" {
		return cmdContextPresetCopy(args[1:])
	}
	if len(args) > 0 && args[0] == "rename" {
		return cmdContextPresetRename(args[1:])
	}
	if len(args) > 0 && args[0] == "update" {
		return cmdContextPresetUpdate(args[1:])
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	if len(args) > 2 || args[0] != "list" && args[0] != "show" || args[0] == "list" && len(args) != 1 || args[0] != "list" && len(args) != 2 {
		return printErr("Invalid preset management arguments", errors.New("use context presets list, show NAME, delete NAME, or rename/copy OLD_NAME NEW_NAME"))
	}
	data, path, err := loadContextPresets()
	if err != nil {
		return printErr("Could not read context presets", err)
	}
	if args[0] == "list" {
		names := []string{}
		for name := range data.Presets {
			names = append(names, name)
		}
		sort.Strings(names)
		return listContextPresets(data, path, names)
	}
	name := args[1]
	if err := validateProfileName(name); err != nil {
		return printErr("Invalid preset name", err)
	}
	preset, exists := data.Presets[name]
	if !exists {
		return unknownSavedName("Unknown context preset", name, data.Presets, func(candidate string) []string { return []string{"context", "presets", "show", candidate} }, []string{"context", "presets", "list"})
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Name   string              `json:"name"`
			Preset localProjectContext `json:"preset"`
			Path   string              `json:"path"`
		}{name, preset, path}))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: project=%s; app=%s; environment=%s\nPath: %s\n", name, preset.Project, contextDefaultLabel(preset.App), contextDefaultLabel(preset.Environment), oneLine(path))
	return 0
}
