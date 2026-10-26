// adr: 20261026110240
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

const claudePluginDir = "../../packaging/claude-plugin"

// agentFacingFiles tell coding agents which commands and tools to use. Paths
// are relative to cmd/gregale.
var agentFacingFiles = []string{
	"../../docs/agents.md",
	"templates/agents.md.tmpl",
	claudePluginDir + "/skills/deploy/SKILL.md",
	claudePluginDir + "/skills/diagnose/SKILL.md",
}

var (
	markdownCodeRE = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```|`([^`\n]+)`")
	mcpToolNameRE  = regexp.MustCompile(`\bgregale_[a-z0-9_]+\b`)
	skillHeaderRE  = regexp.MustCompile(`\A---\nname: ([a-z-]+)\ndescription: (.+)\n---\n`)
	commandWordRE  = regexp.MustCompile(`^[a-z][a-z-]*$`)
)

func TestClaudePluginManifestAndMCPServer(t *testing.T) {
	var manifest struct {
		Name, Version, Description string
	}
	readPluginJSON(t, ".claude-plugin/plugin.json", &manifest)
	if manifest.Name != "gregale" || manifest.Version == "" || manifest.Description == "" {
		t.Fatalf("plugin.json needs name gregale, a version and a description: %+v", manifest)
	}

	var config struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	readPluginJSON(t, ".mcp.json", &config)
	server, ok := config.MCPServers["gregale"]
	if !ok || server.Command != "gregale" || len(server.Args) < 2 || server.Args[0] != "mcp" || server.Args[1] != "serve" {
		t.Fatalf(".mcp.json must run `gregale mcp serve`: %+v", config.MCPServers)
	}
	opts, err := parseMCPServeOptions(server.Args[2:])
	if err != nil {
		t.Fatalf(".mcp.json arguments do not parse: %v", err)
	}
	if opts.transport != "stdio" || opts.allowWrites {
		t.Fatalf("the plugin's server must be read-only stdio; got transport %q, allowWrites %v", opts.transport, opts.allowWrites)
	}
}

func TestClaudePluginSkillsHaveFrontmatter(t *testing.T) {
	skills, err := filepath.Glob(filepath.Join(claudePluginDir, "skills", "*", "SKILL.md"))
	if err != nil || len(skills) == 0 {
		t.Fatalf("no skills found: %v", err)
	}
	for _, path := range skills {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		header := skillHeaderRE.FindSubmatch(content)
		if header == nil {
			t.Errorf("%s must start with name and description frontmatter", path)
			continue
		}
		if dir := filepath.Base(filepath.Dir(path)); string(header[1]) != dir {
			t.Errorf("%s: name %q must match its directory %q", path, header[1], dir)
		}
	}
}

// TestAgentFacingCommandsExist fails when docs, the AGENTS.md template or a
// skill tells an agent to run a command the CLI does not offer to customers.
// A word after a command with subcommands must be a subcommand or an
// obvious placeholder (`<app>`, `my-api`), so a renamed verb is caught too.
func TestAgentFacingCommandsExist(t *testing.T) {
	for _, path := range agentFacingFiles {
		for _, line := range markdownCodeLines(t, path) {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field != "gregale" {
					continue
				}
				name, next := commandAfterGlobals(fields[i+1:])
				if name == "" {
					continue
				}
				command, ok := lookupCliCommand(name)
				if !ok || command.Audience != cliAudienceCustomer {
					t.Errorf("%s: `gregale %s` is not a customer command (in %q)", path, name, line)
					continue
				}
				if next == "" || len(command.Subcommands) == 0 || command.SubcommandsAfterPositionals ||
					strings.HasPrefix(next, "<") || next == "my-api" {
					continue
				}
				if !hasSubcommand(command, next) {
					t.Errorf("%s: `gregale %s %s` names no subcommand (in %q)", path, name, next, line)
				}
			}
		}
	}
}

// TestAgentFacingMCPToolsExist fails when a skill or doc names a management
// MCP tool the server no longer registers.
func TestAgentFacingMCPToolsExist(t *testing.T) {
	tools := managementToolNames(t)
	for _, path := range agentFacingFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range mcpToolNameRE.FindAllString(string(content), -1) {
			if !tools[name] {
				t.Errorf("%s names MCP tool %s, which the management server does not register", path, name)
			}
		}
	}
}

func readPluginJSON(t *testing.T, name string, target any) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(claudePluginDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func markdownCodeLines(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, match := range markdownCodeRE.FindAllStringSubmatch(string(content), -1) {
		lines = append(lines, strings.Split(match[1]+match[2], "\n")...)
	}
	return lines
}

// commandAfterGlobals skips the global options agents are told to use and
// returns the command name and the word after it. It returns no name when
// `gregale` is not followed by a command, as in `claude mcp add gregale --`.
func commandAfterGlobals(fields []string) (string, string) {
	for len(fields) > 0 {
		switch fields[0] {
		case "--non-interactive", "--json", "-j":
			fields = fields[1:]
		case "--profile":
			if len(fields) < 2 {
				return "", ""
			}
			fields = fields[2:]
		default:
			if !commandWordRE.MatchString(fields[0]) {
				return "", ""
			}
			if len(fields) > 1 && !strings.HasPrefix(fields[1], "-") {
				return fields[0], fields[1]
			}
			return fields[0], ""
		}
	}
	return "", ""
}

func hasSubcommand(command cliCommand, name string) bool {
	for _, sub := range command.Subcommands {
		if sub.Name == name {
			return true
		}
	}
	return false
}

func managementToolNames(t *testing.T) map[string]bool {
	t.Helper()
	const base = "http://127.0.0.1:1"
	server, err := mcpmanagement.NewServer(api.NewClient(base, "test-token"), mcpmanagement.Options{
		APIBase: base, Token: "test-token", Version: "test", AllowWrites: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	session, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "plugin-test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	names := map[string]bool{}
	params := &mcp.ListToolsParams{}
	for {
		page, err := client.ListTools(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range page.Tools {
			names[tool.Name] = true
		}
		if page.NextCursor == "" {
			return names
		}
		params.Cursor = page.NextCursor
	}
}
