package api

import (
	"context"
	"net/url"
)

// CompanionDeclaration contains configuration only. It never contains command
// strings, environment values, sealed ciphertext, secret references or health.
type CompanionDeclaration struct {
	Name            string               `json:"name"`
	Image           string               `json:"image,omitempty"`
	Preset          string               `json:"preset,omitempty"`
	Type            string               `json:"type"`
	RAMMB           int                  `json:"ram_mb"`
	CPUMillicores   int                  `json:"cpu_millicores"`
	ScratchMB       int                  `json:"scratch_mb"`
	DiskIOProfile   string               `json:"disk_io_profile,omitempty"`
	Port            int                  `json:"port"`
	PrimaryIngress  bool                 `json:"primary_ingress"`
	Essential       *bool                `json:"essential,omitempty"`
	DependsOn       []WorkloadDependency `json:"depends_on"`
	EnvironmentKeys []string             `json:"environment_keys"`
}

// DeploymentCompanionsResponse identifies one owned immutable declaration set.
// State is available (including known empty) or unavailable (not falsely empty).
// No execution health is inferred from declarations or absence of telemetry.
type DeploymentCompanionsResponse struct {
	AccountID    string                 `json:"account_id"`
	AppID        string                 `json:"app_id"`
	AppSlug      string                 `json:"app_slug"`
	DeploymentID string                 `json:"deployment_id"`
	Scope        string                 `json:"scope"`
	State        string                 `json:"state"`
	Reason       string                 `json:"reason,omitempty"`
	Companions   []CompanionDeclaration `json:"companions"`
}

// GetDeploymentCompanions reads declared metadata; it does not probe workloads.
func (c *Client) GetDeploymentCompanions(ctx context.Context, id string) (DeploymentCompanionsResponse, error) {
	var out DeploymentCompanionsResponse
	return out, c.do(ctx, "GET", "/v1/deployments/"+url.PathEscape(id)+"/companions", nil, &out)
}
