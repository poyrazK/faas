// adr: 20261026110240
package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

// chatgptPluginDir is the Agent Plugins package listed in ChatGPT and Codex.
const chatgptPluginDir = "../../packaging/chatgpt-plugin"

// chatgptPlugin is the part of plugin.json OpenAI's review reads.
type chatgptPlugin struct {
	Schema      string   `json:"$schema"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	Extensions  struct {
		OpenAI struct {
			Interface struct {
				DisplayName       string   `json:"displayName"`
				ShortDescription  string   `json:"shortDescription"`
				LongDescription   string   `json:"longDescription"`
				DeveloperName     string   `json:"developerName"`
				Category          string   `json:"category"`
				Capabilities      []string `json:"capabilities"`
				WebsiteURL        string   `json:"websiteURL"`
				SupportURL        string   `json:"supportURL"`
				PrivacyPolicyURL  string   `json:"privacyPolicyURL"`
				TermsOfServiceURL string   `json:"termsOfServiceURL"`
				DefaultPrompt     []string `json:"defaultPrompt"`
				BrandColor        string   `json:"brandColor"`
				BrandColorDark    string   `json:"brandColorDark"`
				ComposerIcon      string   `json:"composerIcon"`
				Logo              string   `json:"logo"`
			} `json:"interface"`
			OnboardingSkill string `json:"onboardingSkill"`
			Review          struct {
				TestCases struct {
					Positive []struct {
						Description, Prompt string
						ToolsTriggered      string `json:"tools_triggered"`
						ExpectedBehavior    string `json:"expected_behavior"`
					} `json:"positive"`
					Negative []struct {
						Description, Prompt string
					} `json:"negative"`
				} `json:"test_cases"`
			} `json:"review"`
		} `json:"com.openai"`
	} `json:"extensions"`
}

var (
	pluginNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	hexColorRE   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// TestChatGPTPluginManifest checks the limits OpenAI's submission enforces,
// so a rejected upload is caught here rather than in the portal.
func TestChatGPTPluginManifest(t *testing.T) {
	var p chatgptPlugin
	readChatGPTPluginJSON(t, "plugin.json", &p)
	if p.Schema != "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" {
		t.Errorf("$schema = %q, want the Agent Plugins 1.0.0 manifest schema", p.Schema)
	}
	if p.Name != "gregale" || !pluginNameRE.MatchString(p.Name) || p.Version == "" {
		t.Errorf("name must be gregale and version set: %q %q", p.Name, p.Version)
	}
	if p.Description == "" || len(p.Description) > 4000 || len(p.Keywords) > 25 {
		t.Errorf("description must be 1-4000 characters and keywords at most 25")
	}

	ui := p.Extensions.OpenAI.Interface
	for _, field := range []struct {
		name, value string
		max         int
	}{
		{"displayName", ui.DisplayName, 30},
		{"shortDescription", ui.ShortDescription, 30},
		{"longDescription", ui.LongDescription, 4000},
		{"developerName", ui.DeveloperName, 80},
		{"category", ui.Category, 80},
	} {
		if field.value == "" || len([]rune(field.value)) > field.max {
			t.Errorf("interface.%s must be 1-%d characters: %q", field.name, field.max, field.value)
		}
	}
	// Listing guidelines: no "MCP" or "plugin" in the name, and no prices or
	// comparisons with other products in the descriptions.
	for _, word := range []string{"mcp", "plugin"} {
		if strings.Contains(strings.ToLower(ui.DisplayName+" "+ui.ShortDescription), word) {
			t.Errorf("displayName and shortDescription must not say %q", word)
		}
	}
	listing := strings.ToLower(p.Description + " " + ui.ShortDescription + " " + ui.LongDescription)
	for _, word := range []string{"€", "$", "price", "pricing", "free plan", "cheaper", "faster than", "better than", " vs "} {
		if strings.Contains(listing, word) {
			t.Errorf("the listing must not mention prices or comparisons: found %q", word)
		}
	}
	for name, url := range map[string]string{
		"websiteURL": ui.WebsiteURL, "supportURL": ui.SupportURL,
		"privacyPolicyURL": ui.PrivacyPolicyURL, "termsOfServiceURL": ui.TermsOfServiceURL,
	} {
		if !strings.HasPrefix(url, "https://") || len(url) > 1024 {
			t.Errorf("interface.%s must be an HTTPS URL: %q", name, url)
		}
	}
	if len(ui.DefaultPrompt) == 0 || len(ui.DefaultPrompt) > 3 {
		t.Errorf("defaultPrompt needs 1-3 prompts, has %d", len(ui.DefaultPrompt))
	}
	for _, prompt := range ui.DefaultPrompt {
		if len([]rune(prompt)) > 128 {
			t.Errorf("defaultPrompt %q is over 128 characters", prompt)
		}
	}
	for _, color := range []string{ui.BrandColor, ui.BrandColorDark} {
		if !hexColorRE.MatchString(color) {
			t.Errorf("brand color %q must be #RRGGBB", color)
		}
	}
	for _, icon := range []string{ui.Logo, ui.ComposerIcon} {
		checkChatGPTPluginIcon(t, icon)
	}
	if skill := p.Extensions.OpenAI.OnboardingSkill; !strings.HasPrefix(skill, "./skills/") {
		t.Errorf("onboardingSkill %q must be a packaged skill", skill)
	} else if _, err := os.Stat(filepath.Join(chatgptPluginDir, skill)); err != nil {
		t.Errorf("onboardingSkill: %v", err)
	}
}

// TestChatGPTPluginReviewCases checks the review cases OpenAI requires: five
// positive cases whose tools the server registers, and three negative ones.
func TestChatGPTPluginReviewCases(t *testing.T) {
	var p chatgptPlugin
	readChatGPTPluginJSON(t, "plugin.json", &p)
	cases := p.Extensions.OpenAI.Review.TestCases
	if len(cases.Positive) != 5 || len(cases.Negative) != 3 {
		t.Fatalf("review needs exactly 5 positive and 3 negative test cases, has %d and %d", len(cases.Positive), len(cases.Negative))
	}
	tools := managementToolNames(t)
	for _, c := range cases.Positive {
		if c.Description == "" || c.Prompt == "" || c.ToolsTriggered == "" || c.ExpectedBehavior == "" {
			t.Errorf("positive case %q needs description, prompt, tools_triggered and expected_behavior", c.Description)
		}
		for _, name := range strings.Split(c.ToolsTriggered, ",") {
			name = strings.TrimSpace(name)
			if _, hosted := mcpmanagement.ToolsetOf(name); !tools[name] && !hosted {
				t.Errorf("positive case %q triggers %s, which the management server does not register", c.Description, name)
			}
		}
	}
	for _, c := range cases.Negative {
		if c.Description == "" || c.Prompt == "" {
			t.Errorf("negative case %q needs a description and a prompt", c.Description)
		}
	}
}

// TestChatGPTPluginMCPServer pins the package to the hosted server with
// OAuth and no credentials: the person chooses at sign-in what ChatGPT may do.
func TestChatGPTPluginMCPServer(t *testing.T) {
	var config struct {
		Schema     string `json:"$schema"`
		MCPServers map[string]struct {
			Type       string         `json:"type"`
			URL        string         `json:"url"`
			Headers    map[string]any `json:"headers"`
			Extensions struct {
				OpenAI struct {
					Auth map[string]any `json:"auth"`
				} `json:"com.openai"`
			} `json:"extensions"`
		} `json:"mcpServers"`
	}
	readChatGPTPluginJSON(t, "mcp.json", &config)
	if config.Schema != "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json" {
		t.Errorf("$schema = %q, want the Agent Plugins 1.0.0 MCP schema", config.Schema)
	}
	server, ok := config.MCPServers["gregale"]
	if !ok || len(config.MCPServers) != 1 || server.Type != "streamable-http" || server.URL != hostedMCPURL || len(server.Headers) != 0 {
		t.Fatalf("mcp.json must name only the hosted server %s over streamable-http, with no headers: %+v", hostedMCPURL, config.MCPServers)
	}
	auth := server.Extensions.OpenAI.Auth
	client, _ := auth["client"].(map[string]any)
	if auth["type"] != "oauth" || client["mode"] != "cimd" || len(client) != 1 {
		t.Errorf("auth must be OAuth with CIMD registration and no client credentials: %+v", auth)
	}
}

// TestChatGPTPluginSkills checks the skill headers, and that the skills work
// on a default connection: ChatGPT has no shell for the CLI, so a tool
// outside the core toolset may appear only next to the "Diagnose and
// recover" choice that adds it.
func TestChatGPTPluginSkills(t *testing.T) {
	skills, err := filepath.Glob(filepath.Join(chatgptPluginDir, "skills", "*", "SKILL.md"))
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
		if !slices.Contains(agentFacingFiles, path) {
			t.Errorf("%s must be in agentFacingFiles so its commands and tools are checked", path)
		}
		for _, block := range markdownBlocks(string(content)) {
			if strings.Contains(block, "Diagnose and recover") {
				continue
			}
			for _, name := range mcpToolNameRE.FindAllString(block, -1) {
				if set, _ := mcpmanagement.ToolsetOf(name); set != mcpmanagement.ToolsetCore {
					t.Errorf("%s: %s is not a core tool; name the \"Diagnose and recover\" choice in the same item (in %q)", path, name, block)
				}
			}
		}
	}
}

func checkChatGPTPluginIcon(t *testing.T, path string) {
	t.Helper()
	if !strings.HasPrefix(path, "./assets/") || !strings.HasSuffix(path, ".png") {
		t.Errorf("icon %q must be a PNG under ./assets/", path)
		return
	}
	content, err := os.ReadFile(filepath.Join(chatgptPluginDir, path))
	if err != nil {
		t.Error(err)
		return
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	if cfg.Width != cfg.Height || cfg.Width < 48 || len(content) > 5<<20 {
		t.Errorf("%s must be square, at least 48 px and at most 5 MiB: %dx%d, %d bytes", path, cfg.Width, cfg.Height, len(content))
	}
}

func readChatGPTPluginJSON(t *testing.T, name string, target any) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(chatgptPluginDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
