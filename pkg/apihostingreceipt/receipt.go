// Package apihostingreceipt defines the durable, machine-readable evidence
// captured when an API deployment becomes ready.
package apihostingreceipt

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

const SchemaVersion = 1

const (
	SmokeVerified                   = "verified"
	SmokeFailed                     = "failed"
	SmokeSkipped                    = "skipped"
	VerificationHTTPHealth          = "http_health"
	VerificationRouteConnectivity   = "route_connectivity"
	VerificationAPIRouteContract    = "api_route_contract"
	AuthenticationPlatformChallenge = "platform_challenge"
)

const (
	RouteCheckSetVerified    = "verified"
	RouteCheckSetFailed      = "failed"
	RouteCheckSetUnavailable = "unavailable"
	RouteCheckSourceOpenAPI  = "app_openapi"
	MaxAPIRouteChecks        = 10
)

// Source contains non-sensitive provenance. It intentionally excludes source
// paths, environment values, and customer code.
type Source struct {
	Kind        string `json:"kind,omitempty"`
	URL         string `json:"url,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	ImageDigest string `json:"image_digest,omitempty"`
}

type Artifact struct {
	RootfsKey   string `json:"rootfs_key,omitempty"`
	RootfsBytes int64  `json:"rootfs_bytes,omitempty"`
}

type SmokeResult struct {
	// Verification names the promise checked. Empty retains the legacy HTTP
	// health contract. Connectivity does not establish endpoint health.
	Verification string `json:"verification,omitempty"`
	// Authentication describes probe access, not customer/anonymous access.
	Authentication string         `json:"authentication,omitempty"`
	Status         string         `json:"status"`
	Path           string         `json:"path,omitempty"`
	DeploymentID   string         `json:"deployment_id,omitempty"`
	StatusCode     int            `json:"status_code,omitempty"`
	LatencyMS      int64          `json:"latency_ms,omitempty"`
	VerifiedAt     time.Time      `json:"verified_at,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	ErrorCode      string         `json:"error_code,omitempty"`
	Error          string         `json:"error,omitempty"`
	RouteChecks    *RouteCheckSet `json:"route_checks,omitempty"`
	// VerificationContract is the app's gregale.verify.yaml report for this
	// candidate (ADR-20261122220000); absent when the app has no contract or
	// the capability is not released to it.
	VerificationContract *verifycontract.Report `json:"verification_contract,omitempty"`
	// MCPContract is the tool contract gate's check of a listed MCP
	// server's candidate (ADR-20261127191645); absent for other apps.
	MCPContract *MCPContractReport `json:"mcp_contract,omitempty"`
}

// MCPContractReport is a candidate's comparison with the MCP tool contract
// its listing promised.
type MCPContractReport struct {
	Verdict        string              `json:"verdict"`
	BaselineSHA256 string              `json:"baseline_sha256"`
	ContractSHA256 string              `json:"contract_sha256,omitempty"`
	Overridden     bool                `json:"overridden,omitempty"`
	Changes        []MCPContractChange `json:"changes,omitempty"`
	// ToolScan summarizes the candidate's tool safety scan
	// (ADR-20261128135329); nil when its catalog could not be listed.
	ToolScan *MCPToolScanReport `json:"tool_scan,omitempty"`
}

// MCPToolScanReport is the receipt's view of a tool safety scan: counts, and
// the high findings the baseline had not reviewed.
type MCPToolScanReport struct {
	RuleSet    int                  `json:"rule_set"`
	High       int                  `json:"high"`
	Medium     int                  `json:"medium"`
	Low        int                  `json:"low"`
	NewHigh    int                  `json:"new_high"`
	Overridden bool                 `json:"overridden,omitempty"`
	Findings   []MCPToolScanFinding `json:"findings,omitempty"`
}

// MCPToolScanFinding is one mcptoolscan.Finding (same JSON shape).
type MCPToolScanFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Excerpt  string `json:"excerpt,omitempty"`
}

// MCPContractChange is one mcphosting.ContractChange (same JSON shape).
type MCPContractChange struct {
	Tool             string `json:"tool,omitempty"`
	Resource         string `json:"resource,omitempty"`
	ResourceTemplate string `json:"resource_template,omitempty"`
	Prompt           string `json:"prompt,omitempty"`
	Capability       string `json:"capability,omitempty"`
	Path             string `json:"path"`
	Kind             string `json:"kind"`
	Severity         string `json:"severity"`
}

// APIRouteProbe is a bounded, read-only request selected from an app's
// explicitly annotated OpenAPI operations.
type APIRouteProbe struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// RouteCheckSet records the source contract and individual candidate results.
// DocumentSHA256 binds the check to the exact imported OpenAPI document.
type RouteCheckSet struct {
	Source         string             `json:"source"`
	DocumentSHA256 string             `json:"document_sha256,omitempty"`
	Status         string             `json:"status"`
	Checks         []RouteCheckResult `json:"checks"`
}

// RouteCheckResult contains only bounded, non-secret evidence from one
// authenticated candidate response. Request and response bodies are omitted.
type RouteCheckResult struct {
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     string    `json:"status"`
	StatusCode int       `json:"status_code,omitempty"`
	LatencyMS  int64     `json:"latency_ms,omitempty"`
	VerifiedAt time.Time `json:"verified_at,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func (checks RouteCheckSet) Validate() error {
	if checks.Source != RouteCheckSourceOpenAPI {
		return fmt.Errorf("invalid route check source %q", checks.Source)
	}
	if decoded, err := hex.DecodeString(checks.DocumentSHA256); err != nil || len(decoded) != 32 {
		return errors.New("route check document_sha256 must be a SHA-256 digest")
	}
	switch checks.Status {
	case RouteCheckSetVerified, RouteCheckSetFailed, RouteCheckSetUnavailable:
	default:
		return fmt.Errorf("invalid route check set status %q", checks.Status)
	}
	if len(checks.Checks) == 0 || len(checks.Checks) > MaxAPIRouteChecks {
		return fmt.Errorf("route check count must be between 1 and %d", MaxAPIRouteChecks)
	}
	for i, check := range checks.Checks {
		if err := ValidateAPIRouteProbe(APIRouteProbe{Method: check.Method, Path: check.Path}); err != nil {
			return fmt.Errorf("route check %d needs a GET method and path", i)
		}
		switch check.Status {
		case SmokeVerified, SmokeFailed, SmokeSkipped:
		default:
			return fmt.Errorf("invalid route check %d status %q", i, check.Status)
		}
	}
	return nil
}

type Receipt struct {
	SchemaVersion int                      `json:"schema_version"`
	DeploymentID  string                   `json:"deployment_id"`
	AppID         string                   `json:"app_id"`
	AppURL        string                   `json:"app_url,omitempty"`
	Source        Source                   `json:"source"`
	Profile       frameworkprofile.Profile `json:"profile"`
	Artifact      Artifact                 `json:"artifact"`
	Smoke         SmokeResult              `json:"smoke"`
}

func (r Receipt) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", r.SchemaVersion)
	}
	if strings.TrimSpace(r.DeploymentID) == "" {
		return errors.New("deployment_id is required")
	}
	if strings.TrimSpace(r.AppID) == "" {
		return errors.New("app_id is required")
	}
	if r.Profile.Version == "" {
		return errors.New("profile.version is required")
	}
	// Empty means the workload has no HTTP readiness endpoint and the runtime
	// should use its TCP listener gate. Source/framework profiles normally set
	// /healthz (or an inferred path); direct OCI images intentionally leave it
	// empty unless the customer supplied an explicit override.
	if r.Profile.HealthPath != "" && !strings.HasPrefix(r.Profile.HealthPath, "/") {
		return errors.New("profile.health_path must start with /")
	}
	switch r.Smoke.Status {
	case SmokeVerified, SmokeFailed, SmokeSkipped:
	default:
		return fmt.Errorf("invalid smoke status %q", r.Smoke.Status)
	}
	switch r.Smoke.Verification {
	case "", VerificationHTTPHealth, VerificationRouteConnectivity:
	default:
		return fmt.Errorf("invalid smoke verification %q", r.Smoke.Verification)
	}
	if r.Smoke.Authentication != "" && r.Smoke.Authentication != AuthenticationPlatformChallenge {
		return fmt.Errorf("invalid smoke authentication %q", r.Smoke.Authentication)
	}
	if checks := r.Smoke.RouteChecks; checks != nil {
		if err := checks.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func Encode(r Receipt) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

func Decode(data []byte) (Receipt, error) {
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return Receipt{}, err
	}
	if err := r.Validate(); err != nil {
		return Receipt{}, err
	}
	return r, nil
}
