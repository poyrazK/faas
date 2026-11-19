package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func writeTestFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInitAgentsAgentsMD(t *testing.T) {
	section, err := templates.AgentsSection("my-api")
	if err != nil {
		t.Fatal(err)
	}
	oldSection, err := templates.AgentsSection("old-name")
	if err != nil {
		t.Fatal(err)
	}
	guide := "# Agent instructions\n\n" + templates.AgentsGuideMarker + " It was created from the hello-node starter.\n"
	tests := []struct {
		name     string
		existing *string
		want     string
		action   string
	}{
		{name: "creates a new file", want: "# Agent instructions\n\n" + section, action: agentFileCreated},
		{name: "appends after existing text", existing: ptr("# Rules\n\nUse tabs.\n\n\n"), want: "# Rules\n\nUse tabs.\n\n" + section, action: agentFileUpdated},
		{name: "fills an empty file", existing: ptr(""), want: section, action: agentFileUpdated},
		{name: "replaces the marked section in place", existing: ptr("# Rules\n\n" + oldSection + "\n## Testing\n\nRun make test.\n"), want: "# Rules\n\n" + section + "\n## Testing\n\nRun make test.\n", action: agentFileUpdated},
		{name: "keeps a current section", existing: ptr("# Rules\n\n" + section), want: "# Rules\n\n" + section, action: agentFileUnchanged},
		{name: "keeps the guide gregale init wrote", existing: &guide, want: guide, action: agentFileUnchanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.existing != nil {
				writeTestFile(t, dir, "AGENTS.md", *tt.existing)
			}
			receipt := runInitAgentsJSON(t, dir, "my-api", false)
			if got := receipt.Files[0]; got.Path != "AGENTS.md" || got.Action != tt.action {
				t.Fatalf("AGENTS.md change = %+v, want action %s", got, tt.action)
			}
			if got := readTestFile(t, dir, "AGENTS.md"); got != tt.want {
				t.Fatalf("AGENTS.md =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestInitAgentsClaudeImport(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		want     string
		action   string
	}{
		{name: "adds the import", existing: "# Project\n", want: "@AGENTS.md\n\n# Project\n", action: agentFileUpdated},
		{name: "keeps an existing import", existing: "# Project\n\n  @AGENTS.md\n", want: "# Project\n\n  @AGENTS.md\n", action: agentFileUnchanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, "CLAUDE.md", tt.existing)
			receipt := runInitAgentsJSON(t, dir, "my-api", false)
			if len(receipt.Files) != 2 || receipt.Files[1].Path != "CLAUDE.md" || receipt.Files[1].Action != tt.action {
				t.Fatalf("files = %+v, want CLAUDE.md %s", receipt.Files, tt.action)
			}
			if got := readTestFile(t, dir, "CLAUDE.md"); got != tt.want {
				t.Fatalf("CLAUDE.md = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInitAgentsLeavesClaudeMDAbsent(t *testing.T) {
	dir := t.TempDir()
	receipt := runInitAgentsJSON(t, dir, "my-api", false)
	if len(receipt.Files) != 1 {
		t.Fatalf("files = %+v, want only AGENTS.md", receipt.Files)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md stat = %v, want it not created", err)
	}
}

func TestInitAgentsGeminiSettings(t *testing.T) {
	settings := filepath.Join(".gemini", "settings.json")
	created := "{\n  \"context\": {\n    \"fileName\": [\"AGENTS.md\", \"GEMINI.md\"]\n  }\n}\n"
	tests := []struct {
		name    string
		project map[string]string
		action  string
		want    string
	}{
		{name: "no gemini settings", action: ""},
		// Antigravity reads GEMINI.md and AGENTS.md by itself.
		{name: "GEMINI.md alone", project: map[string]string{"GEMINI.md": "# Notes\n"}, action: ""},
		{name: "a .gemini folder", project: map[string]string{filepath.Join(".gemini", "commands", "test.toml"): "prompt = \"x\"\n"}, action: agentFileCreated, want: created},
		{name: "settings already read AGENTS.md", project: map[string]string{settings: `{"context":{"fileName":"AGENTS.md"}}`}, action: agentFileUnchanged, want: `{"context":{"fileName":"AGENTS.md"}}`},
		{name: "older contextFileName key", project: map[string]string{settings: `{"contextFileName":["GEMINI.md","AGENTS.md"]}`}, action: agentFileUnchanged, want: `{"contextFileName":["GEMINI.md","AGENTS.md"]}`},
		{name: "settings without AGENTS.md stay untouched", project: map[string]string{settings: `{"theme":"dark"}`}, action: agentFileManual, want: `{"theme":"dark"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A ~/.gemini folder (Antigravity keeps files there too) is
			// no reason to write project settings.
			t.Setenv("HOME", t.TempDir())
			if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".gemini"), 0o755); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for name, body := range tt.project {
				writeTestFile(t, dir, name, body)
			}
			receipt := runInitAgentsJSON(t, dir, "my-api", false)
			var action string
			for _, change := range receipt.Files {
				if change.Path == settings {
					action = change.Action
				}
			}
			if action != tt.action {
				t.Fatalf("settings action = %q, want %q (files %+v)", action, tt.action, receipt.Files)
			}
			if tt.want == "" {
				if _, err := os.Stat(filepath.Join(dir, settings)); !os.IsNotExist(err) {
					t.Fatalf("settings stat = %v, want none", err)
				}
				return
			}
			if got := readTestFile(t, dir, settings); got != tt.want {
				t.Fatalf("settings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInitAgentsDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "CLAUDE.md", "# Project\n")
	if err := os.Mkdir(filepath.Join(dir, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := runInitAgentsJSON(t, dir, "my-api", true)
	if !receipt.DryRun || len(receipt.Files) != 3 {
		t.Fatalf("receipt = %+v, want a dry run planning 3 files", receipt)
	}
	for _, change := range receipt.Files {
		if change.Action != agentFileCreated && change.Action != agentFileUpdated {
			t.Fatalf("%s action = %s, want a planned write", change.Path, change.Action)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md stat = %v, want nothing written", err)
	}
	if got := readTestFile(t, dir, "CLAUDE.md"); got != "# Project\n" {
		t.Fatalf("CLAUDE.md = %q, want it unchanged", got)
	}
}

func TestInitAgentsApp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Orders API")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := runInitAgentsJSON(t, dir, "", false).App; got != "orders-api" {
		t.Fatalf("app = %q, want the directory name as a slug", got)
	}
	if got := runInitAgentsJSON(t, dir, "billing", false).App; got != "billing" {
		t.Fatalf("app = %q, want --name", got)
	}
	if err := rememberInitApp(dir, "linked-api"); err != nil {
		t.Fatal(err)
	}
	if got := runInitAgentsJSON(t, dir, "", false).App; got != "linked-api" {
		t.Fatalf("app = %q, want the linked app", got)
	}
	if !strings.Contains(readTestFile(t, dir, "AGENTS.md"), "deploy --name linked-api`") {
		t.Fatal("AGENTS.md does not name the linked app after a second run")
	}
}

func TestInitAgentsRejectsABadName(t *testing.T) {
	if code := runInitAgents(t.TempDir(), "My API", false, &bytes.Buffer{}); code == 0 {
		t.Fatal("exit 0 for an invalid --name")
	}
}

func runInitAgentsJSON(t *testing.T, dir, name string, dryRun bool) initAgentsReceipt {
	t.Helper()
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })
	var out bytes.Buffer
	if code := runInitAgents(dir, name, dryRun, &out); code != 0 {
		t.Fatalf("runInitAgents exit %d: %s", code, out.String())
	}
	var receipt initAgentsReceipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	return receipt
}

func ptr(s string) *string { return &s }
