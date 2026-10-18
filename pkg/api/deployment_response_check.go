package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const DeploymentResponseCheckProtocolVersion = 1
const DeploymentResponseCheckCapabilitiesControlPath = "/v1/internal/deployment-response-check-capabilities"

const (
	DeploymentResponseCheckGatewayUnsupported  = "gateway_unsupported"
	DeploymentResponseCheckGatewayUnavailable  = "gateway_unavailable"
	DeploymentResponseCheckGatewayUnconfigured = "gateway_unconfigured"
)

// DeploymentResponseCheckCapabilities reports installed protocol support and
// forwarding configuration, not workload capacity or a guest health verdict.
type DeploymentResponseCheckCapabilities struct {
	ProtocolVersion   int    `json:"protocol_version"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

func (c DeploymentResponseCheckCapabilities) SupportsCurrentProtocol() bool {
	return c.ProtocolVersion == DeploymentResponseCheckProtocolVersion && c.Available && c.UnavailableReason == ""
}

func (c *Client) GetDeploymentResponseCheckCapabilities(ctx context.Context) (DeploymentResponseCheckCapabilities, error) {
	var out DeploymentResponseCheckCapabilities
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return out, c.doWithClientAndIdempotencyKey(ctx, &client, http.MethodGet, "/v1/deployments/response-check/capabilities", nil, &out, "")
}

// DeploymentResponseCheckRequest selects one GET against an owned deployment.
// Named scopes require the catalog lifetime observed by the caller.
type DeploymentResponseCheckRequest struct {
	AppID         string `json:"app_id"`
	Scope         string `json:"scope"`
	ProjectID     string `json:"project_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Path          string `json:"path"`
}

func (r DeploymentResponseCheckRequest) Validate() error {
	for index, id := range []string{r.AppID, r.ProjectID, r.EnvironmentID} {
		if id == "" && index > 0 {
			continue
		}
		if !ValidDeploymentResponseCheckID(id) {
			return errors.New("identities must be nonempty platform UUIDs")
		}
	}
	if r.Scope == "" || ValidateScope(r.Scope) != nil {
		return errors.New("scope must explicitly select one runtime environment")
	}
	if r.Scope != DefaultEnvScope && (r.ProjectID == "" || r.EnvironmentID == "") {
		return errors.New("named scopes require project_id and environment_id")
	}
	if r.EnvironmentID != "" && r.ProjectID == "" {
		return errors.New("environment_id requires project_id")
	}
	u, err := url.Parse(r.Path)
	if err != nil || len(r.Path) > DeploymentResponseCheckMaxPathBytes || !strings.HasPrefix(r.Path, "/") || strings.HasPrefix(r.Path, "//") || strings.Contains(r.Path, "\\") || strings.ContainsRune(r.Path, '#') || u.Host != "" || u.Scheme != "" || strings.IndexFunc(r.Path, unicode.IsControl) >= 0 {
		return errors.New("path must be a bounded app-relative GET path without a fragment or controls")
	}
	return nil
}

// DeploymentResponseCheckResult is ephemeral response evidence, not a public
// ingress or customer-authentication verdict. No workload headers are returned.
type DeploymentResponseCheckResult struct {
	AppID         string `json:"app_id"`
	DeploymentID  string `json:"deployment_id"`
	Scope         string `json:"scope"`
	ProjectID     string `json:"project_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	InstanceID    string `json:"instance_id"`
	HTTPStatus    int    `json:"http_status"`
	Body          string `json:"body"`
	BodyTruncated bool   `json:"body_truncated"`
}

func (r DeploymentResponseCheckResult) Matches(request DeploymentResponseCheckRequest, deploymentID string) bool {
	return ValidDeploymentResponseCheckID(r.InstanceID) && r.AppID == request.AppID && r.DeploymentID == deploymentID && r.Scope == request.Scope && r.ProjectID == request.ProjectID && r.EnvironmentID == request.EnvironmentID && r.HTTPStatus >= 200 && r.HTTPStatus <= 599 && len(r.Body) <= DeploymentResponseCheckMaxBodyBytes
}

// ValidDeploymentResponseCheckID accepts both UUID encodings used by the API,
// without rewriting their wire identity or accepting revisions and aliases.
func ValidDeploymentResponseCheckID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && (len(id) == 32 || len(id) == 36) && strings.ToLower(id) == id
}

func (c *Client) CheckDeploymentResponse(ctx context.Context, slug, deploymentID string, request DeploymentResponseCheckRequest) (DeploymentResponseCheckResult, error) {
	var out DeploymentResponseCheckResult
	if err := request.Validate(); err != nil {
		return out, err
	}
	if !ValidAppSlug(slug) || !ValidDeploymentResponseCheckID(deploymentID) {
		return out, errors.New("valid app and exact deployment UUID are required")
	}
	endpoint := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + deploymentID + "/response-check"
	return out, c.do(ctx, http.MethodPost, endpoint, request, &out)
}
