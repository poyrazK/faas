package mcplisting_test

// adr: 20261126175800 — a listing's endpoint stays on the app's host, its
// fields are bounded, and it renders install links and a registry
// server.json.

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcplisting"
	"github.com/onebox-faas/faas/pkg/state"
)

var hosts = []string{"search.gregale.app", "mcp.acme.dev"}

func valid() api.PutMCPListingRequest {
	return api.PutMCPListingRequest{Title: " Search ", Summary: "Find things.", Endpoint: "https://mcp.acme.dev/mcp",
		Tools: []api.MCPListingTool{{Name: "search", Description: "Search the web"}}, RegistryName: "io.github.acme/search"}
}

func TestValidate(t *testing.T) {
	l, p := mcplisting.Validate(valid(), "acct", "app", hosts)
	if p != nil || l.Title != "Search" || len(l.Tools) != 1 || l.Tools[0].Name != "search" || l.AppID != "app" {
		t.Fatalf("valid listing: %+v %v", l, p)
	}
	cases := map[string]func(*api.PutMCPListingRequest){
		"no title":       func(r *api.PutMCPListingRequest) { r.Title = " " },
		"long title":     func(r *api.PutMCPListingRequest) { r.Title = strings.Repeat("x", api.MCPListingTitleMax+1) },
		"long summary":   func(r *api.PutMCPListingRequest) { r.Summary = strings.Repeat("x", api.MCPListingSummaryMax+1) },
		"http endpoint":  func(r *api.PutMCPListingRequest) { r.Endpoint = "http://mcp.acme.dev/mcp" },
		"foreign host":   func(r *api.PutMCPListingRequest) { r.Endpoint = "https://evil.example/mcp" },
		"credentials":    func(r *api.PutMCPListingRequest) { r.Endpoint = "https://u:p@mcp.acme.dev/mcp" },
		"query":          func(r *api.PutMCPListingRequest) { r.Endpoint = "https://mcp.acme.dev/mcp?x=1" },
		"bad registry":   func(r *api.PutMCPListingRequest) { r.RegistryName = "search" },
		"bad tool name":  func(r *api.PutMCPListingRequest) { r.Tools[0].Name = "has space" },
		"duplicate tool": func(r *api.PutMCPListingRequest) { r.Tools = append(r.Tools, r.Tools[0]) },
		"long description": func(r *api.PutMCPListingRequest) {
			r.Tools[0].Description = strings.Repeat("x", api.MCPListingToolDescriptionMax+1)
		},
		"tools over the byte cap": func(r *api.PutMCPListingRequest) {
			r.Tools = nil
			for i := 0; i < api.MCPListingMaxTools; i++ {
				// Every field is within its rune limit; the stored JSON is not.
				r.Tools = append(r.Tools, api.MCPListingTool{Name: "t" + strconv.Itoa(i), Description: strings.Repeat("é", api.MCPListingToolDescriptionMax)})
			}
		},
		"too many tools": func(r *api.PutMCPListingRequest) {
			r.Tools = nil
			for i := 0; i <= api.MCPListingMaxTools; i++ {
				r.Tools = append(r.Tools, api.MCPListingTool{Name: "t" + strconv.Itoa(i)})
			}
		},
	}
	for name, mutate := range cases {
		req := valid()
		req.Tools = append([]api.MCPListingTool{}, req.Tools...)
		mutate(&req)
		if _, p := mcplisting.Validate(req, "acct", "app", hosts); p == nil || p.Status != 422 {
			t.Errorf("%s: problem = %v, want 422", name, p)
		}
	}
}

// Limit problems name the limit and the observed value.
func TestValidateLimitFields(t *testing.T) {
	req := valid()
	req.Endpoint = "https://mcp.acme.dev/" + strings.Repeat("x", api.MCPListingEndpointMax)
	if _, p := mcplisting.Validate(req, "acct", "app", hosts); p == nil || p.Limit == nil || *p.Limit != api.MCPListingEndpointMax || p.Observed == nil {
		t.Fatalf("long endpoint problem = %+v", p)
	}
	req = valid()
	req.Tools = []api.MCPListingTool{{Name: strings.Repeat("x", api.MCPListingToolNameMax+1)}}
	if _, p := mcplisting.Validate(req, "acct", "app", hosts); p == nil || p.Limit == nil || *p.Limit != api.MCPListingToolNameMax {
		t.Fatalf("long tool name problem = %+v", p)
	}
}

// A stored endpoint serves only while the app still answers on its host.
func TestOnAppHost(t *testing.T) {
	if !mcplisting.OnAppHost("https://mcp.acme.dev/mcp", hosts) {
		t.Fatal("endpoint on a current host refused")
	}
	if mcplisting.OnAppHost("https://mcp.acme.dev/mcp", []string{"search.gregale.app"}) {
		t.Fatal("endpoint on a removed custom domain still served")
	}
}

func TestInstall(t *testing.T) {
	in := mcplisting.Install("search", "https://mcp.acme.dev/mcp")
	cursor, err := url.Parse(in.CursorURL)
	if err != nil || cursor.Host != "cursor.com" || cursor.Query().Get("name") != "search" {
		t.Fatalf("cursor url = %q", in.CursorURL)
	}
	raw, err := base64.StdEncoding.DecodeString(cursor.Query().Get("config"))
	if err != nil || string(raw) != `{"url":"https://mcp.acme.dev/mcp"}` {
		t.Fatalf("cursor config = %s %v", raw, err)
	}
	vscode, err := url.Parse(in.VSCodeURL)
	if err != nil || vscode.Host != "vscode.dev" || vscode.Query().Get("config") != `{"type":"http","url":"https://mcp.acme.dev/mcp"}` {
		t.Fatalf("vscode url = %q", in.VSCodeURL)
	}
	if string(in.ClientConfig) != `{"mcpServers":{"search":{"url":"https://mcp.acme.dev/mcp"}}}` {
		t.Fatalf("client config = %s", in.ClientConfig)
	}
}

func TestServerJSON(t *testing.T) {
	l := state.MCPListing{Title: "Search", Summary: strings.Repeat("word ", 40), Endpoint: "https://mcp.acme.dev/mcp",
		RegistryName: "io.github.acme/search", Version: 3}
	raw, err := mcplisting.ServerJSON(l, "search", "https://api.gregale.dev")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema      string `json:"$schema"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		WebsiteURL  string `json:"websiteUrl"`
		Remotes     []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"remotes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != mcplisting.RegistrySchema || doc.Name != "io.github.acme/search" || doc.Version != "1.0.3" ||
		len([]rune(doc.Description)) > api.MCPListingRegistryDescriptionMax ||
		doc.WebsiteURL != "https://api.gregale.dev/v1/consumer-signup/search/listing/page" ||
		len(doc.Remotes) != 1 || doc.Remotes[0].Type != "streamable-http" || doc.Remotes[0].URL != l.Endpoint {
		t.Fatalf("server.json = %s", raw)
	}
}
