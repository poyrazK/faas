// adr: 20261026110240
package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// packaging/cursor-plugin is the Cursor Marketplace plugin. Its skills are the
// Claude Code plugin's skills, copied unchanged, so the command and tool names
// claude_plugin_test.go checks there hold here too.

const (
	cursorPluginDir = "../../packaging/cursor-plugin"
	claudeSkillsDir = "../../packaging/claude-plugin/skills"
)

func TestCursorPluginManifest(t *testing.T) {
	var manifest struct {
		Name, Version, Description, Homepage, Repository, License, Logo string
		Author                                                          struct{ Name string }
	}
	readCursorPluginJSON(t, ".cursor-plugin/plugin.json", &manifest)
	if manifest.Name != "gregale" || manifest.Version == "" || manifest.Description == "" ||
		manifest.Author.Name == "" || manifest.License != "Apache-2.0" || manifest.Repository == "" {
		t.Fatalf("plugin.json needs name gregale, a version, description, author, repository and the Apache-2.0 license: %+v", manifest)
	}
	for _, rel := range []string{manifest.Logo, "LICENSE", "README.md"} {
		if rel == "" {
			t.Fatal("plugin.json needs a logo")
		}
		if _, err := os.Stat(filepath.Join(cursorPluginDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestCursorPluginMCPServerIsHosted(t *testing.T) {
	var config struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	readCursorPluginJSON(t, "mcp.json", &config)
	server, ok := config.MCPServers["gregale"]
	if !ok || len(config.MCPServers) != 1 || len(server) != 1 || server["url"] != hostedMCPServerURL {
		t.Fatalf("mcp.json must name only the hosted server %s: %+v", hostedMCPServerURL, config.MCPServers)
	}
}

func TestCursorPluginSkillsMatchClaudePlugin(t *testing.T) {
	claude, err := filepath.Glob(filepath.Join(claudeSkillsDir, "*", "SKILL.md"))
	if err != nil || len(claude) == 0 {
		t.Fatalf("no Claude plugin skills: %v", err)
	}
	cursor, err := filepath.Glob(filepath.Join(cursorPluginDir, "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cursor) != len(claude) {
		t.Fatalf("Cursor plugin has %d skills, Claude plugin %d; copy packaging/claude-plugin/skills", len(cursor), len(claude))
	}
	for _, src := range claude {
		rel, _ := filepath.Rel(claudeSkillsDir, src)
		want, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(cursorPluginDir, "skills", rel))
		if err != nil {
			t.Errorf("skills/%s: %v", rel, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("packaging/cursor-plugin/skills/%s differs from the Claude plugin's; copy it again", rel)
		}
	}
}

var cursorRuleFrontmatterRE = regexp.MustCompile(`\A---\ndescription: .+\nalwaysApply: (true|false)\n---\n`)

func TestCursorPluginRulesHaveFrontmatter(t *testing.T) {
	rules, err := filepath.Glob(filepath.Join(cursorPluginDir, "rules", "*.mdc"))
	if err != nil || len(rules) == 0 {
		t.Fatalf("no rules found: %v", err)
	}
	for _, path := range rules {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !cursorRuleFrontmatterRE.Match(content) {
			t.Errorf("%s needs description and alwaysApply frontmatter", path)
		}
	}
}

func readCursorPluginJSON(t *testing.T, name string, target any) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(cursorPluginDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
