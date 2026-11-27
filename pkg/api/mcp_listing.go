package api

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// A sold MCP server's public listing (ADR-20261126175800).

// MCPListingTool is one tool on a listing.
type MCPListingTool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// PutMCPListingRequest publishes or republishes an app's listing. Tools is
// the snapshot of the live server's tools/list the seller's CLI took.
type PutMCPListingRequest struct {
	Title    string           `json:"title"`
	Summary  string           `json:"summary,omitempty"`
	Endpoint string           `json:"endpoint"`
	Tools    []MCPListingTool `json:"tools"`
	// RegistryName is the server's MCP registry name, such as
	// io.github.acme/search; empty serves no server.json.
	RegistryName string `json:"registry_name,omitempty"`
	// ShowUsage shows the open customer count on the public page.
	ShowUsage bool `json:"show_usage,omitempty"`
	// Contract is the live server's format-2 catalog contract
	// (mcphosting.MarshalContract) the CLI took with the tools. Production
	// deploys are then checked against it (ADR-20261127191645); without it
	// the listing is not gated.
	Contract json.RawMessage `json:"contract,omitempty"`
	// ProbeSecret names an app secret holding a bearer token for a server
	// that checks credentials itself; the gate sends it when listing a
	// candidate's tools.
	ProbeSecret string `json:"probe_secret,omitempty"`
}

// MCPListingResponse is the owner's view of a published listing.
type MCPListingResponse struct {
	App          string           `json:"app"`
	Title        string           `json:"title"`
	Summary      string           `json:"summary"`
	Endpoint     string           `json:"endpoint"`
	Tools        []MCPListingTool `json:"tools"`
	RegistryName string           `json:"registry_name,omitempty"`
	ShowUsage    bool             `json:"show_usage"`
	Version      int              `json:"version"`
	PublishedAt  time.Time        `json:"published_at"`
	// PageURL is the public page; ServerJSONURL is set with a registry name.
	PageURL       string `json:"page_url"`
	ServerJSONURL string `json:"server_json_url,omitempty"`
	// ContractSHA256 is the published tool contract's digest; empty when
	// the listing is not gated.
	ContractSHA256 string `json:"contract_sha256,omitempty"`
	ProbeSecret    string `json:"probe_secret,omitempty"`
}

// MCPListingInstall is how a client adds the server.
type MCPListingInstall struct {
	CursorURL string `json:"cursor_url"`
	VSCodeURL string `json:"vscode_url"`
	// ClientConfig is a JSON client configuration for the server, as
	// {"mcpServers": {"<app>": {"url": "<endpoint>"}}}.
	ClientConfig json.RawMessage `json:"client_config"`
}

// PublicMCPListingResponse is what the public page shows.
type PublicMCPListingResponse struct {
	App         string               `json:"app"`
	Title       string               `json:"title"`
	Summary     string               `json:"summary"`
	Endpoint    string               `json:"endpoint"`
	Tools       []MCPListingTool     `json:"tools"`
	Plans       []ConsumerSignupPlan `json:"plans"`
	TrialDays   int                  `json:"trial_days,omitempty"`
	Livemode    bool                 `json:"livemode"`
	SignupOpen  bool                 `json:"signup_open"`
	SignupURL   string               `json:"signup_url,omitempty"`
	Install     MCPListingInstall    `json:"install"`
	PublishedAt time.Time            `json:"published_at"`
	// Customers is the open customer count, only when the seller shows it.
	Customers *int64 `json:"customers,omitempty"`
	// Changelog lists the tool contract changes shipped since the listing
	// was published, newest first (ADR-20261127191645).
	Changelog []MCPContractChangelogEntry `json:"changelog,omitempty"`
}

// GetMCPListing reads an app's published listing.
func (c *Client) GetMCPListing(ctx context.Context, app string) (MCPListingResponse, error) {
	var out MCPListingResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-listing", nil, &out)
}

// PutMCPListing publishes or republishes an app's listing.
func (c *Client) PutMCPListing(ctx context.Context, app string, req PutMCPListingRequest) (MCPListingResponse, error) {
	var out MCPListingResponse
	return out, c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(app)+"/mcp-listing", req, &out)
}

// DeleteMCPListing unpublishes an app's listing.
func (c *Client) DeleteMCPListing(ctx context.Context, app string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+url.PathEscape(app)+"/mcp-listing", nil, nil)
}

// GetPublicMCPListing reads a published listing. It needs no credential.
func (c *Client) GetPublicMCPListing(ctx context.Context, app string) (PublicMCPListingResponse, error) {
	var out PublicMCPListingResponse
	return out, c.do(ctx, "GET", "/v1/consumer-signup/"+url.PathEscape(app)+"/listing", nil, &out)
}

// GetMCPListingServerJSON reads a listing's MCP registry server.json. It
// needs no credential.
func (c *Client) GetMCPListingServerJSON(ctx context.Context, app string) (json.RawMessage, error) {
	var out json.RawMessage
	return out, c.do(ctx, "GET", "/v1/consumer-signup/"+url.PathEscape(app)+"/server.json", nil, &out)
}
