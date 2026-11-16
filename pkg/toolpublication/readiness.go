package toolpublication

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func ReadinessReview(p api.MCPReadinessPolicy) *api.ToolPublicationReadiness {
	if !p.Enabled {
		return nil
	}
	out := &api.ToolPublicationReadiness{PolicyRevision: p.Revision, Endpoint: p.Endpoint, MaxAgeSeconds: p.MaxAgeSeconds, Roles: []api.ToolPublicationReadinessRole{}}
	for _, r := range p.Roles {
		out.Roles = append(out.Roles, api.ToolPublicationReadinessRole{Name: r.Name, ProtocolVersion: r.ProtocolVersion, CatalogFormat: r.CatalogFormat, CatalogSHA256: r.CatalogSHA256})
	}
	return out
}

func ReadinessMatches(review *api.ToolPublicationReadiness, p api.MCPReadinessPolicy) bool {
	a, _ := json.Marshal(review)
	b, _ := json.Marshal(ReadinessReview(p))
	return string(a) == string(b)
}

func ValidReadinessReview(in api.ToolPublicationIntent) bool {
	r := in.Readiness
	if r == nil {
		return true
	}
	if !in.PlatformVerification || r.Endpoint != in.EndpointPath {
		return false
	}
	if _, err := uuid.Parse(r.PolicyRevision); err != nil {
		return false
	}
	p := api.SetMCPReadinessPolicyRequest{Scope: api.DefaultEnvScope, Enabled: true, Endpoint: r.Endpoint, MaxAgeSeconds: r.MaxAgeSeconds, Reason: "publication review"}
	for _, role := range r.Roles {
		p.Roles = append(p.Roles, api.MCPReadinessRole{Name: role.Name, ProtocolVersion: role.ProtocolVersion, CatalogFormat: role.CatalogFormat, CatalogSHA256: role.CatalogSHA256})
	}
	return api.ValidateMCPReadinessPolicy(p) == nil
}

func ReadinessExpectation(v api.ToolPublicationVerification) api.MCPReadinessExpectation {
	out := api.MCPReadinessExpectation{ArtifactSHA256: v.ReadinessArtifactSHA256, EvidenceIDs: map[string]string{}}
	if v.Outcome != nil {
		for _, r := range v.Outcome.Readiness {
			if r.Passed {
				out.EvidenceIDs[r.Role] = r.EvidenceID
			}
		}
	}
	return out
}

func ReadinessEvidenceMatches(p api.MCPReadinessPolicy, artifact string, evidence []api.MCPReadinessEvidence, e api.MCPReadinessExpectation, now time.Time) bool {
	if e.Validate() != nil || !p.Enabled || p.Revision != e.PolicyRevision || artifact != e.ArtifactSHA256 || len(p.Roles) != len(e.EvidenceIDs) {
		return false
	}
	for _, r := range p.Roles {
		found := false
		for _, v := range evidence {
			if v.Role == r.Name && v.ID == e.EvidenceIDs[r.Name] && v.Passed && v.PolicyRevision == p.Revision && v.ArtifactSHA256 == artifact && v.ProtocolVersion == r.ProtocolVersion && v.CatalogFormat == r.CatalogFormat && v.CatalogSHA256 == r.CatalogSHA256 && !v.ObservedAt.After(now) && now.Sub(v.ObservedAt) <= time.Duration(p.MaxAgeSeconds)*time.Second {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func ReadinessResultsMatch(review *api.ToolPublicationReadiness, results []api.ToolPublicationRoleResult) bool {
	if review == nil {
		return len(results) == 0
	}
	if len(results) != len(review.Roles) {
		return false
	}
	for i, r := range review.Roles {
		if results[i].Role != r.Name || !results[i].Passed {
			return false
		}
		if _, err := uuid.Parse(results[i].EvidenceID); err != nil {
			return false
		}
	}
	return true
}
