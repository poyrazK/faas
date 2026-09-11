package main

// commands_config.go owns the non-secret, persistent CLI preferences. The
// bearer token deliberately remains in config.go's OS-keychain/file fallback;
// this file only stores settings that are safe to copy between workstations.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	cliConfigDirName  = "gregale"
	cliConfigFileName = "config.json"
	configKeyAPIBase  = "api-base"
	configKeyJSON     = "json"
)

// cliConfig is intentionally small and non-secret. Adding a credential field
// here would bypass the keychain guarantees in config.go and make `config
// list` a secret-disclosure surface.
type cliConfig struct {
	APIBase string `json:"api_base,omitempty"`
	JSON    *bool  `json:"json,omitempty"`
}

type cliConfigEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// cliConfigPath is separate from tokenPath so the token migration contract
// remains unchanged while preferences get a documented JSON representation.
func cliConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cliConfigDirName, cliConfigFileName), nil
}

func loadCLIConfig() (cliConfig, error) {
	path, err := cliConfigPath()
	if err != nil {
		return cliConfig{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cliConfig{}, nil
	}
	if err != nil {
		return cliConfig{}, err
	}
	var cfg cliConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cliConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.APIBase != "" {
		normalized, err := validateConfigAPIBase(cfg.APIBase)
		if err != nil {
			return cliConfig{}, fmt.Errorf("invalid api_base in %s: %w", path, err)
		}
		cfg.APIBase = normalized
	}
	return cfg, nil
}

// saveCLIConfig writes through a same-directory temporary file, then renames
// it into place. The mode is set before any bytes are written and restored
// after rename so a crash cannot leave a world-readable preference file.
func saveCLIConfig(cfg cliConfig) error {
	if cfg.APIBase != "" {
		normalized, err := validateConfigAPIBase(cfg.APIBase)
		if err != nil {
			return err
		}
		cfg.APIBase = normalized
	}
	path, err := cliConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func validateConfigAPIBase(raw string) (string, error) {
	normalized := normalizeAPIBase(raw)
	u, err := url.Parse(normalized)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("API base must be an http(s) URL without credentials, query parameters, or fragments")
	}
	return normalized, nil
}

func parseConfigBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("value must be true or false (accepted: true|false|on|off|yes|no|1|0), got %q", raw)
	}
}

func configuredJSONPreference() (bool, bool) {
	if raw, ok := os.LookupEnv("FAAS_JSON"); ok && strings.TrimSpace(raw) != "" {
		return jsonBoolTrue(raw), true
	}
	cfg, err := loadCLIConfig()
	if err == nil && cfg.JSON != nil {
		return *cfg.JSON, true
	}
	return false, false
}

func effectiveConfigEntries(cfg cliConfig) []cliConfigEntry {
	apiEntry := cliConfigEntry{Key: configKeyAPIBase, Value: defaultAPIBase, Source: "default"}
	if raw, ok := os.LookupEnv("FAAS_API"); ok && strings.TrimSpace(raw) != "" {
		apiEntry.Value = normalizeAPIBase(raw)
		apiEntry.Source = "env:FAAS_API"
	} else if cfg.APIBase != "" {
		apiEntry.Value = normalizeAPIBase(cfg.APIBase)
		apiEntry.Source = "config"
	}

	jsonEntry := cliConfigEntry{Key: configKeyJSON, Value: "false", Source: "default"}
	if raw, ok := os.LookupEnv("FAAS_JSON"); ok && strings.TrimSpace(raw) != "" {
		jsonEntry.Value = fmt.Sprintf("%t", jsonBoolTrue(raw))
		jsonEntry.Source = "env:FAAS_JSON"
	} else if cfg.JSON != nil {
		jsonEntry.Value = fmt.Sprintf("%t", *cfg.JSON)
		jsonEntry.Source = "config"
	}
	return []cliConfigEntry{apiEntry, jsonEntry}
}

func configEntry(entries []cliConfigEntry, key string) (cliConfigEntry, bool) {
	for _, entry := range entries {
		if entry.Key == key {
			return entry, true
		}
	}
	return cliConfigEntry{}, false
}

func normalizeConfigKey(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "api", "api-base", "api_base", "apiurl", "api-url":
		return configKeyAPIBase
	case "json", "output", "output-json", "output_json":
		return configKeyJSON
	default:
		return ""
	}
}

func cmdConfig(args []string) int {
	parent, _ := lookupCliCommand("config")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale config <get|set|list> ...", "config")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdConfigList(args[1:])
	case "get":
		return cmdConfigGet(args[1:])
	case "set":
		return cmdConfigSet(args[1:])
	default:
		_, _ = fmt.Fprintf(os.Stderr, "gregale config: unknown subcommand %q\n", args[0])
		if suggestion, ok := suggestSubcommand(args[0], parent); ok {
			maybeSuggestSub(suggestion)
		}
		return 1
	}
}

func cmdConfigList(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: gregale config list", "config")
		return 1
	}
	cfg, err := loadCLIConfig()
	if err != nil {
		return printErr("Could not read config", err)
	}
	entries := effectiveConfigEntries(cfg)
	if jsonOutput {
		return jsonOut(writeNDJSON(entries))
	}
	_, _ = fmt.Fprintln(osStdout, "KEY       VALUE                         SOURCE")
	for _, entry := range entries {
		_, _ = fmt.Fprintf(osStdout, "%-9s %-29s %s\n", entry.Key, entry.Value, entry.Source)
	}
	return 0
}

func cmdConfigGet(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale config get <api-base|json>", "config")
		return 1
	}
	key := normalizeConfigKey(args[0])
	if key == "" {
		return printErr("Unknown config key", fmt.Errorf("%q (want api-base or json)", args[0]))
	}
	cfg, err := loadCLIConfig()
	if err != nil {
		return printErr("Could not read config", err)
	}
	entry, _ := configEntry(effectiveConfigEntries(cfg), key)
	if jsonOutput {
		return jsonOut(writeJSON(entry))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %s (source=%s)\n", entry.Key, entry.Value, entry.Source)
	return 0
}

func cmdConfigSet(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale config set <api-base|json> <value>", "config")
		return 1
	}
	key := normalizeConfigKey(args[0])
	if key == "" {
		return printErr("Unknown config key", fmt.Errorf("%q (want api-base or json)", args[0]))
	}
	cfg, err := loadCLIConfig()
	if err != nil {
		return printErr("Could not read config", err)
	}
	switch key {
	case configKeyAPIBase:
		value, err := validateConfigAPIBase(args[1])
		if err != nil {
			return printErr("Invalid api-base", err)
		}
		cfg.APIBase = value
	case configKeyJSON:
		value, err := parseConfigBool(args[1])
		if err != nil {
			return printErr("Invalid json preference", err)
		}
		cfg.JSON = &value
	}
	if err := saveCLIConfig(cfg); err != nil {
		return printErr("Could not save config", err)
	}
	entry, _ := configEntry(effectiveConfigEntries(cfg), key)
	if jsonOutput {
		return jsonOut(writeJSON(entry))
	}
	PrintOK(osStdout, "Saved %s=%s.", key, entry.Value)
	return 0
}
