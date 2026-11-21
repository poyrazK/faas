package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// Publication reviews exclude the policy's credential references and monitors.
type ToolPublicationReadiness struct {
	PolicyRevision string                         `json:"policy_revision"`
	Endpoint       string                         `json:"endpoint"`
	MaxAgeSeconds  int                            `json:"max_age_seconds"`
	Roles          []ToolPublicationReadinessRole `json:"roles"`
}

type ToolPublicationReadinessRole struct {
	Name            string `json:"name"`
	ProtocolVersion string `json:"protocol_version"`
	CatalogFormat   int    `json:"catalog_format"`
	CatalogSHA256   string `json:"catalog_sha256"`
}

type ToolPublicationRoleResult struct {
	Role       string `json:"role"`
	EvidenceID string `json:"evidence_id,omitempty"`
	Passed     bool   `json:"passed"`
}

// MCPReadinessExpectation fences exact observations at traffic admission.
type MCPReadinessExpectation struct {
	PolicyRevision string            `json:"policy_revision"`
	ArtifactSHA256 string            `json:"artifact_sha256"`
	EvidenceIDs    map[string]string `json:"evidence_ids"`
}

func (e MCPReadinessExpectation) Validate() error {
	if _, err := uuid.Parse(e.PolicyRevision); err != nil {
		return errors.New("invalid readiness policy revision")
	}
	if !readinessHashPattern.MatchString(e.ArtifactSHA256) || len(e.EvidenceIDs) == 0 || len(e.EvidenceIDs) > ToolPublicationReadinessMaxRoles {
		return errors.New("invalid readiness expectation")
	}
	for role, id := range e.EvidenceIDs {
		if !readinessRolePattern.MatchString(role) {
			return errors.New("invalid readiness role")
		}
		if _, err := uuid.Parse(id); err != nil {
			return errors.New("invalid readiness evidence identity")
		}
	}
	return nil
}

func (c *Client) PatchDeploymentTrafficIfServingWithMCPReadiness(ctx context.Context, id string, percent int, serving string, expected MCPReadinessExpectation) (DeploymentResponse, error) {
	var out DeploymentResponse
	err := c.doWithHeaders(ctx, http.MethodPatch, "/v1/deployments/"+id+"/traffic", UpdateDeploymentTrafficRequest{TrafficPercent: percent, ExpectedServingDeploymentID: &serving, ExpectedMCPReadiness: &expected}, &out, deploymentPromotionHeaders(ctx))
	return out, err
}
