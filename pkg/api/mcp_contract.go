package api

import (
	"context"
	"net/url"
	"time"
)

// The tool contract gate for sold MCP servers (ADR-20261127191645).

// MCPContractChange is one difference between a candidate's MCP catalog and
// the contract its listing promised (mcphosting.ContractChange).
type MCPContractChange struct {
	Tool             string `json:"tool,omitempty"`
	Resource         string `json:"resource,omitempty"`
	ResourceTemplate string `json:"resource_template,omitempty"`
	Prompt           string `json:"prompt,omitempty"`
	Capability       string `json:"capability,omitempty"`
	Path             string `json:"path"`
	Kind             string `json:"kind"`
	// Severity is informational, needs_review or breaking.
	Severity string `json:"severity"`
}

// MCPContractCheck is one production candidate's comparison. Verdict is
// unchanged, compatible, review, breaking or unverified (the catalog could
// not be listed and an override admitted the candidate).
type MCPContractCheck struct {
	DeploymentID   string              `json:"deployment_id"`
	Verdict        string              `json:"verdict"`
	BaselineSHA256 string              `json:"baseline_sha256"`
	ContractSHA256 string              `json:"contract_sha256,omitempty"`
	Overridden     bool                `json:"overridden"`
	Changes        []MCPContractChange `json:"changes"`
	CheckedAt      time.Time           `json:"checked_at"`
	// AcceptedAt is when the candidate went live; the next candidate is
	// compared with its contract.
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	// ToolScan is the candidate's tool safety scan (ADR-20261128135329);
	// ToolScanNewHigh counts the high findings the baseline had not
	// reviewed, which need a tool_scan override (ToolScanOverridden).
	ToolScan           *MCPToolScan `json:"tool_scan,omitempty"`
	ToolScanNewHigh    int          `json:"tool_scan_new_high,omitempty"`
	ToolScanOverridden bool         `json:"tool_scan_overridden,omitempty"`
}

// MCP contract override kinds (ADR-20261128135329).
const (
	MCPContractOverrideKindContract = "contract"
	MCPContractOverrideKindToolScan = "tool_scan"
)

// MCPContractOverride admits one breaking or unverified candidate (kind
// contract) or one candidate with unreviewed high tool scan findings (kind
// tool_scan).
type MCPContractOverride struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Reason    string    `json:"reason"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// DeploymentID, when set, is the only deployment the override admits.
	DeploymentID string `json:"deployment_id,omitempty"`
}

// MCPContractResponse is GET /v1/apps/{slug}/mcp-contract.
type MCPContractResponse struct {
	App string `json:"app"`
	// BaselineSHA256 is the contract the next production candidate is
	// compared with: the listing's, or the newest accepted candidate's
	// (BaselineDeploymentID) since the listing was published.
	BaselineSHA256       string   `json:"baseline_sha256"`
	BaselineDeploymentID string   `json:"baseline_deployment_id,omitempty"`
	ListingVersion       int      `json:"listing_version"`
	Tools                []string `json:"tools"`
	ProbeSecret          string   `json:"probe_secret,omitempty"`
	// BaselineToolScan is the baseline's tool safety scan; its reviewed
	// high findings do not need a new override.
	BaselineToolScan *MCPToolScan          `json:"baseline_tool_scan,omitempty"`
	Checks           []MCPContractCheck    `json:"checks"`
	Overrides        []MCPContractOverride `json:"overrides"`
}

// CreateMCPContractOverrideRequest records why the next breaking candidate
// may ship. ExpiresInSeconds defaults to MCPContractOverrideDefaultTTL and is
// at most MCPContractOverrideMaxTTL. DeploymentID binds the override to one
// of the app's deployments; without it the app's next breaking production
// candidate uses it.
type CreateMCPContractOverrideRequest struct {
	Reason           string `json:"reason"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
	DeploymentID     string `json:"deployment_id,omitempty"`
	// Kind is contract (the default: admits a breaking or unverified
	// candidate) or tool_scan (admits unreviewed high tool scan findings).
	Kind string `json:"kind,omitempty"`
}

// MCPContractChangelogEntry is one accepted change on a public listing: the
// changed tool names, never schemas or override reasons.
type MCPContractChangelogEntry struct {
	Date    time.Time `json:"date"`
	Verdict string    `json:"verdict"`
	Tools   []string  `json:"tools"`
	Changes int       `json:"changes"`
}

// MCPContractChangedWebhookPayload is delivered with mcp.contract.changed
// when a candidate that changed the contract goes live.
type MCPContractChangedWebhookPayload struct {
	AppID          string   `json:"app_id"`
	DeploymentID   string   `json:"deployment_id"`
	Verdict        string   `json:"verdict"`
	BaselineSHA256 string   `json:"baseline_sha256"`
	ContractSHA256 *string  `json:"contract_sha256"`
	Overridden     bool     `json:"overridden"`
	Tools          []string `json:"tools"`
	Changes        int      `json:"changes"`
}

// GetMCPContract reads an app's tool contract, recent checks and open
// overrides.
func (c *Client) GetMCPContract(ctx context.Context, app string) (MCPContractResponse, error) {
	var out MCPContractResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-contract", nil, &out)
}

// CreateMCPContractOverride lets the app's next breaking or unverified
// production candidate ship.
func (c *Client) CreateMCPContractOverride(ctx context.Context, app string, req CreateMCPContractOverrideRequest) (MCPContractOverride, error) {
	var out MCPContractOverride
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(app)+"/mcp-contract/overrides", req, &out)
}
