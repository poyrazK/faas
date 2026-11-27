// Package mcplisting validates and renders a sold MCP server's public
// listing (ADR-20261126175800): the snapshot a seller publishes, its
// install links and its MCP registry server.json.
package mcplisting

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
)

// DocsURL documents listings.
const DocsURL = "https://gregale.dev/docs/api-monetization#mcp-listing"

// RegistrySchema is the MCP registry server.json schema the file follows.
const RegistrySchema = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"

var (
	registryName = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
	toolName     = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	probeSecret  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// Validate checks a publish request and returns the listing to store. hosts
// are the app's own hosts (platform URL and default custom domain): the
// endpoint must be https on one of them.
func Validate(req api.PutMCPListingRequest, accountID, appID string, hosts []string) (state.MCPListing, *api.Problem) {
	req.Title, req.Summary = strings.TrimSpace(req.Title), strings.TrimSpace(req.Summary)
	if req.Title == "" {
		return state.MCPListing{}, invalid("title is required")
	}
	if p := tooLong("title", req.Title, api.MCPListingTitleMax); p != nil {
		return state.MCPListing{}, p
	}
	if p := tooLong("summary", req.Summary, api.MCPListingSummaryMax); p != nil {
		return state.MCPListing{}, p
	}
	if p := checkEndpoint(req.Endpoint, hosts); p != nil {
		return state.MCPListing{}, p
	}
	if req.RegistryName != "" && !registryName.MatchString(req.RegistryName) {
		return state.MCPListing{}, invalid("registry_name must look like io.github.<user>/<server> or <reverse-dns>/<server>")
	}
	if len(req.Tools) > api.MCPListingMaxTools {
		return state.MCPListing{}, invalid(fmt.Sprintf("at most %d tools", api.MCPListingMaxTools)).
			WithLimit(api.MCPListingMaxTools, int64(len(req.Tools)))
	}
	tools := make([]state.MCPListingTool, 0, len(req.Tools))
	seen := map[string]bool{}
	for _, t := range req.Tools {
		if n := utf8.RuneCountInString(t.Name); n > api.MCPListingToolNameMax {
			return state.MCPListing{}, invalid(fmt.Sprintf("a tool name is longer than %d characters", api.MCPListingToolNameMax)).
				WithLimit(api.MCPListingToolNameMax, int64(n))
		}
		if !toolName.MatchString(t.Name) {
			return state.MCPListing{}, invalid(fmt.Sprintf("tool name %q must be 1-%d letters, digits, _ . or -", t.Name, api.MCPListingToolNameMax))
		}
		if seen[t.Name] {
			return state.MCPListing{}, invalid(fmt.Sprintf("tool %q is listed twice", t.Name))
		}
		seen[t.Name] = true
		if p := tooLong("tool "+t.Name+" title", t.Title, api.MCPListingToolTitleMax); p != nil {
			return state.MCPListing{}, p
		}
		if p := tooLong("tool "+t.Name+" description", t.Description, api.MCPListingToolDescriptionMax); p != nil {
			return state.MCPListing{}, p
		}
		tools = append(tools, state.MCPListingTool{Name: t.Name, Title: strings.TrimSpace(t.Title), Description: strings.TrimSpace(t.Description)})
	}
	// The stored snapshot has its own byte cap: many long descriptions, or
	// multi-byte text, can pass every per-field limit and still not fit.
	if raw, err := json.Marshal(tools); err != nil || len(raw) > api.MCPListingToolsMaxBytes {
		return state.MCPListing{}, invalid(fmt.Sprintf("the tools take more than %d bytes; shorten their descriptions or list fewer", api.MCPListingToolsMaxBytes)).
			WithLimit(api.MCPListingToolsMaxBytes, int64(len(raw)))
	}
	listing := state.MCPListing{AccountID: accountID, AppID: appID, Title: req.Title, Summary: req.Summary, Endpoint: req.Endpoint,
		Tools: tools, RegistryName: req.RegistryName, ShowUsage: req.ShowUsage, ProbeSecret: req.ProbeSecret}
	if req.ProbeSecret != "" && !probeSecret.MatchString(req.ProbeSecret) {
		return state.MCPListing{}, invalid("probe_secret must name an app secret: letters, digits and _, not starting with a digit")
	}
	if len(req.Contract) == 0 {
		if req.ProbeSecret != "" {
			return state.MCPListing{}, invalid("probe_secret is used only with a contract")
		}
		return listing, nil
	}
	var p *api.Problem
	listing.Contract, listing.ContractSHA256, p = canonicalContract(req.Contract, seen)
	if p != nil {
		return state.MCPListing{}, p
	}
	return listing, nil
}

// canonicalContract parses the tool contract (ADR-20261127191645) and
// returns its canonical form and digest, so equal catalogs compare equal
// whatever the CLI's key order. Every listed tool must be in it.
func canonicalContract(raw json.RawMessage, listed map[string]bool) (json.RawMessage, string, *api.Problem) {
	if len(raw) > api.MCPListingContractMaxBytes {
		return nil, "", invalid(fmt.Sprintf("the contract takes more than %d bytes", api.MCPListingContractMaxBytes)).
			WithLimit(api.MCPListingContractMaxBytes, int64(len(raw)))
	}
	contract, err := mcphosting.ReadContract(bytes.NewReader(raw))
	if err != nil {
		return nil, "", invalid("contract is not an MCP catalog contract: " + err.Error())
	}
	canonical, err := mcphosting.MarshalContract(contract)
	if err != nil || len(canonical) > api.MCPListingContractMaxBytes {
		return nil, "", invalid(fmt.Sprintf("the contract takes more than %d bytes", api.MCPListingContractMaxBytes)).
			WithLimit(api.MCPListingContractMaxBytes, int64(len(canonical)))
	}
	inContract := map[string]bool{}
	for _, t := range contract.Tools {
		inContract[t.Name] = true
	}
	for name := range listed {
		if !inContract[name] {
			return nil, "", invalid(fmt.Sprintf("tool %q is listed but not in the contract; publish both from the same server", name))
		}
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

func checkEndpoint(raw string, hosts []string) *api.Problem {
	if n := utf8.RuneCountInString(raw); n > api.MCPListingEndpointMax {
		return invalid(fmt.Sprintf("endpoint is longer than %d characters", api.MCPListingEndpointMax)).
			WithLimit(api.MCPListingEndpointMax, int64(n))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return invalid("endpoint must be an https URL without credentials, query or fragment")
	}
	for _, h := range hosts {
		if h != "" && strings.EqualFold(u.Host, h) {
			return nil
		}
	}
	return invalid("endpoint must be on the app's own host (" + strings.Join(nonEmpty(hosts), " or ") + ")")
}

func nonEmpty(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func tooLong(field, value string, max int) *api.Problem {
	if n := utf8.RuneCountInString(value); n > max {
		return invalid(fmt.Sprintf("%s is longer than %d characters", field, max)).WithLimit(int64(max), int64(n))
	}
	return nil
}

func invalid(detail string) *api.Problem {
	return api.NewProblem(422, api.CodeValidation, "Invalid MCP listing", detail).WithDocs(DocsURL)
}

// PageURL is the listing's public page; ServerJSONURL its registry file.
func PageURL(origin, slug string) string {
	return strings.TrimRight(origin, "/") + "/v1/consumer-signup/" + url.PathEscape(slug) + "/listing/page"
}

func ServerJSONURL(origin, slug string) string {
	return strings.TrimRight(origin, "/") + "/v1/consumer-signup/" + url.PathEscape(slug) + "/server.json"
}

// Tools converts the stored snapshot to the wire form.
func Tools(in []state.MCPListingTool) []api.MCPListingTool {
	out := make([]api.MCPListingTool, 0, len(in))
	for _, t := range in {
		out = append(out, api.MCPListingTool{Name: t.Name, Title: t.Title, Description: t.Description})
	}
	return out
}

// Response is the owner's view.
func Response(l state.MCPListing, slug, origin string) api.MCPListingResponse {
	out := api.MCPListingResponse{App: slug, Title: l.Title, Summary: l.Summary, Endpoint: l.Endpoint, Tools: Tools(l.Tools),
		RegistryName: l.RegistryName, ShowUsage: l.ShowUsage, Version: l.Version, PublishedAt: l.PublishedAt,
		PageURL: PageURL(origin, slug), ContractSHA256: l.ContractSHA256, ProbeSecret: l.ProbeSecret}
	if l.RegistryName != "" {
		out.ServerJSONURL = ServerJSONURL(origin, slug)
	}
	return out
}

// Install builds the one-click links and a JSON client configuration.
func Install(slug, endpoint string) api.MCPListingInstall {
	cursor, _ := json.Marshal(map[string]string{"url": endpoint})
	vscode, _ := json.Marshal(map[string]string{"type": "http", "url": endpoint})
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{slug: map[string]string{"url": endpoint}}})
	name := url.QueryEscape(slug)
	return api.MCPListingInstall{
		CursorURL:    "https://cursor.com/en/install-mcp?name=" + name + "&config=" + url.QueryEscape(base64.StdEncoding.EncodeToString(cursor)),
		VSCodeURL:    "https://vscode.dev/redirect/mcp/install?name=" + name + "&config=" + url.QueryEscape(string(vscode)),
		ClientConfig: config,
	}
}

// ServerJSON renders the MCP registry server.json; the version follows the
// listing's publish count.
func ServerJSON(l state.MCPListing, slug, origin string) ([]byte, error) {
	description := l.Summary
	if description == "" {
		description = l.Title
	}
	if utf8.RuneCountInString(description) > api.MCPListingRegistryDescriptionMax {
		r := []rune(description)[:api.MCPListingRegistryDescriptionMax-1]
		description = strings.TrimSpace(string(r)) + "…"
	}
	doc := map[string]any{
		"$schema":     RegistrySchema,
		"name":        l.RegistryName,
		"title":       l.Title,
		"description": description,
		"websiteUrl":  PageURL(origin, slug),
		"version":     fmt.Sprintf("1.0.%d", l.Version),
		"remotes":     []map[string]string{{"type": "streamable-http", "url": l.Endpoint}},
	}
	return json.MarshalIndent(doc, "", "  ")
}
