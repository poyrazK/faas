package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveCLIConfigWritesRestrictedAtomicFile(t *testing.T) {
	setupHermeticTokensEnv(t)
	jsonPref := true
	if err := saveCLIConfig(cliConfig{APIBase: "api.example.com/", JSON: &jsonPref}); err != nil {
		t.Fatalf("saveCLIConfig: %v", err)
	}

	path, err := cliConfigPath()
	if err != nil {
		t.Fatalf("cliConfigPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got cliConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if got.APIBase != "https://api.example.com" || got.JSON == nil || !*got.JSON {
		t.Fatalf("config = %+v, want normalized API base and json=true", got)
	}
	tmpFiles, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*.tmp"))
	if err != nil {
		t.Fatalf("glob temporary config files: %v", err)
	}
	if len(tmpFiles) != 0 {
		t.Fatalf("temporary config files remain: %v", tmpFiles)
	}
}

func TestAPIBaseConfigPrecedence(t *testing.T) {
	setupHermeticTokensEnv(t)
	t.Setenv("FAAS_API", "")
	if err := saveCLIConfig(cliConfig{APIBase: "https://config.example"}); err != nil {
		t.Fatalf("saveCLIConfig: %v", err)
	}
	if got := apiBase(); got != "https://config.example" {
		t.Fatalf("apiBase() = %q, want config value", got)
	}
	t.Setenv("FAAS_API", "env.example/ ")
	if got := apiBase(); got != "https://env.example" {
		t.Fatalf("apiBase() = %q, want env override", got)
	}
}

func TestConfiguredJSONPreferencePrecedence(t *testing.T) {
	setupHermeticTokensEnv(t)
	t.Setenv("FAAS_JSON", "")
	pref := true
	if err := saveCLIConfig(cliConfig{JSON: &pref}); err != nil {
		t.Fatalf("saveCLIConfig: %v", err)
	}
	if got, ok := configuredJSONPreference(); !ok || !got {
		t.Fatalf("configuredJSONPreference() = %t, %t; want true, true", got, ok)
	}
	t.Setenv("FAAS_JSON", "off")
	if got, ok := configuredJSONPreference(); !ok || got {
		t.Fatalf("configuredJSONPreference() = %t, %t; want false, true from env", got, ok)
	}
}

func TestCmdConfigSetGetList(t *testing.T) {
	setupHermeticTokensEnv(t)
	t.Setenv("FAAS_API", "")
	t.Setenv("FAAS_JSON", "")
	oldOut := osStdout
	var out strings.Builder
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdConfig([]string{"set", "api-base", "localhost:8080/"}); code != 0 {
		t.Fatalf("config set exit = %d", code)
	}
	if !strings.Contains(out.String(), "Saved api-base=https://localhost:8080.") {
		t.Fatalf("set output = %q", out.String())
	}
	out.Reset()
	if code := cmdConfig([]string{"get", "api-base"}); code != 0 {
		t.Fatalf("config get exit = %d", code)
	}
	if got := out.String(); !strings.Contains(got, "api-base: https://localhost:8080 (source=config)") {
		t.Fatalf("get output = %q", got)
	}
	out.Reset()
	if code := cmdConfig([]string{"list"}); code != 0 {
		t.Fatalf("config list exit = %d", code)
	}
	if got := out.String(); !strings.Contains(got, "api-base") || !strings.Contains(got, "SOURCE") {
		t.Fatalf("list output = %q", got)
	}
}

func TestCmdConfigJSONOutputIsStableAndNonSecret(t *testing.T) {
	setupHermeticTokensEnv(t)
	t.Setenv("FAAS_API", "")
	t.Setenv("FAAS_JSON", "")
	oldOut := osStdout
	var out strings.Builder
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	pref := true
	if err := saveCLIConfig(cliConfig{APIBase: "https://config.example", JSON: &pref}); err != nil {
		t.Fatalf("saveCLIConfig: %v", err)
	}
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	if code := cmdConfig([]string{"list"}); code != 0 {
		t.Fatalf("config list --json exit = %d", code)
	}
	dec := json.NewDecoder(strings.NewReader(out.String()))
	var entries []cliConfigEntry
	for {
		var entry cliConfigEntry
		err := dec.Decode(&entry)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode JSON entry: %v; output=%q", err, out.String())
		}
		entries = append(entries, entry)
	}
	if len(entries) != 2 || entries[0].Key != configKeyAPIBase || entries[1].Key != configKeyJSON {
		t.Fatalf("entries = %+v, want api-base then json", entries)
	}
	if strings.Contains(out.String(), "token") || strings.Contains(out.String(), "secret") {
		t.Fatalf("config JSON exposed secret-like fields: %q", out.String())
	}
}

func TestCmdConfigRejectsInvalidValuesWithoutWriting(t *testing.T) {
	setupHermeticTokensEnv(t)
	t.Setenv("FAAS_API", "")
	t.Setenv("FAAS_JSON", "")
	if code := cmdConfig([]string{"set", "api-base", "https://valid.example"}); code != 0 {
		t.Fatalf("initial config set exit = %d", code)
	}
	path, err := cliConfigPath()
	if err != nil {
		t.Fatalf("cliConfigPath: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	if code := cmdConfig([]string{"set", "json", "sometimes"}); code == 0 {
		t.Fatal("invalid json preference was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config after invalid set: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("invalid set changed config: before=%q after=%q", before, after)
	}
	if code := cmdConfig([]string{"set", "api-base", "https://user:pass@valid.example"}); code == 0 {
		t.Fatal("API base with embedded credentials was accepted")
	}
	afterCredentials, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config after credentials set: %v", err)
	}
	if string(afterCredentials) != string(before) {
		t.Fatalf("credentials set changed config: before=%q after=%q", before, afterCredentials)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".config-invalid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected invalid temp file: %v", err)
	}
}
