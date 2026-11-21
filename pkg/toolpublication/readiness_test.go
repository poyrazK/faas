package toolpublication

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestReadinessEvidenceRequiresExactFreshObservations(t *testing.T) {
	now := time.Now().UTC()
	revision, id := uuid.NewString(), uuid.NewString()
	hash := strings.Repeat("a", 64)
	p := api.MCPReadinessPolicy{Enabled: true, Revision: revision, MaxAgeSeconds: 60, Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 2, CatalogSHA256: hash}}}
	base := api.MCPReadinessEvidence{ID: id, Role: "reader", PolicyRevision: revision, ArtifactSHA256: hash, ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 2, CatalogSHA256: hash, Passed: true, ObservedAt: now}
	expected := api.MCPReadinessExpectation{PolicyRevision: revision, ArtifactSHA256: hash, EvidenceIDs: map[string]string{"reader": id}}
	if !ReadinessEvidenceMatches(p, hash, []api.MCPReadinessEvidence{base}, expected, now) {
		t.Fatal("fresh exact observation rejected")
	}
	for _, mode := range []string{"stale", "future", "failed", "replaced", "catalog", "protocol", "policy", "artifact"} {
		t.Run(mode, func(t *testing.T) {
			e := base
			switch mode {
			case "stale":
				e.ObservedAt = now.Add(-61 * time.Second)
			case "future":
				e.ObservedAt = now.Add(time.Nanosecond)
			case "failed":
				e.Passed = false
			case "replaced":
				e.ID = uuid.NewString()
			case "catalog":
				e.CatalogSHA256 = strings.Repeat("b", 64)
			case "protocol":
				e.ProtocolVersion = api.MCPLegacyProtocolVersion
			case "policy":
				e.PolicyRevision = uuid.NewString()
			case "artifact":
				e.ArtifactSHA256 = strings.Repeat("b", 64)
			}
			if ReadinessEvidenceMatches(p, hash, []api.MCPReadinessEvidence{e}, expected, now) {
				t.Fatal("changed observation authorized promotion")
			}
		})
	}
}
