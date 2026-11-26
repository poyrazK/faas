// adr: 20261026110240
package main_test

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"testing"
)

// The MCP registry listing and the one-click install links in the docs all
// point clients at the hosted server. They live outside the code that serves
// it, so this test keeps them on hostedMCPServerURL.

const (
	mcpRegistryServerJSON = "../../packaging/mcp-registry/server.json"
	// hostedMCPServerURL is Gregale's hosted management MCP server; the
	// plugin's .mcp.json is pinned to the same URL in claude_plugin_test.go.
	hostedMCPServerURL = "https://api.gregale.dev/mcp"
)

func TestMCPRegistryListingNamesHostedServer(t *testing.T) {
	content, err := os.ReadFile(mcpRegistryServerJSON)
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Packages    []any  `json:"packages"`
		Remotes     []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"remotes"`
	}
	if err := json.Unmarshal(content, &listing); err != nil {
		t.Fatal(err)
	}
	// GitHub OIDC in mcp-registry-publish.yml can only publish under the
	// repository owner's namespace.
	if listing.Name != "io.github.poyrazK/gregale" {
		t.Errorf("name = %q, want io.github.poyrazK/gregale", listing.Name)
	}
	if listing.Version == "" || listing.Description == "" || len(listing.Description) > 100 {
		t.Errorf("need a version and a description of at most 100 characters: %+v", listing)
	}
	if len(listing.Packages) != 0 || len(listing.Remotes) != 1 ||
		listing.Remotes[0].Type != "streamable-http" || listing.Remotes[0].URL != hostedMCPServerURL {
		t.Errorf("listing must name only the hosted server %s over streamable-http: %+v", hostedMCPServerURL, listing)
	}
}

var (
	cursorInstallLinkRE = regexp.MustCompile(`https://cursor\.com/en/install-mcp\?[^)\s]+`)
	vscodeInstallLinkRE = regexp.MustCompile(`https://vscode\.dev/redirect/mcp/install\?[^)\s]+`)
)

func TestMCPInstallLinksNameHostedServer(t *testing.T) {
	links := 0
	for _, path := range []string{"../../docs/agents.md", "../../docs/cursor.md"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range cursorInstallLinkRE.FindAllString(string(content), -1) {
			links++
			query := installLinkQuery(t, link)
			raw, err := base64.StdEncoding.DecodeString(query.Get("config"))
			if err != nil {
				t.Errorf("%s: %s: config is not base64: %v", path, link, err)
				continue
			}
			checkInstallConfig(t, path, link, query.Get("name"), raw, false)
		}
		for _, link := range vscodeInstallLinkRE.FindAllString(string(content), -1) {
			links++
			query := installLinkQuery(t, link)
			checkInstallConfig(t, path, link, query.Get("name"), []byte(query.Get("config")), true)
		}
	}
	if links == 0 {
		t.Fatal("no one-click install links found in the docs")
	}
}

func installLinkQuery(t *testing.T, link string) url.Values {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("%s: %v", link, err)
	}
	return parsed.Query()
}

func checkInstallConfig(t *testing.T, path, link, name string, raw []byte, wantType bool) {
	t.Helper()
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Errorf("%s: %s: config is not JSON: %v", path, link, err)
		return
	}
	want := map[string]string{"url": hostedMCPServerURL}
	if wantType {
		want["type"] = "http"
	}
	if name != "gregale" || len(config) != len(want) {
		t.Errorf("%s: %s: want name gregale and config %v, got %q %v", path, link, want, name, config)
		return
	}
	for k, v := range want {
		if config[k] != v {
			t.Errorf("%s: %s: config[%q] = %q, want %q", path, link, k, config[k], v)
		}
	}
}
