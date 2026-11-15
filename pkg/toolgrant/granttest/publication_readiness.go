package granttest

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func ConfigurePublicationReadiness(t *testing.T, p *PublicationFixture, version string) api.MCPReadinessPolicy {
	t.Helper()
	reader := p.PrimaryTools.Load().([]mcphosting.Tool)
	writer := append(append([]mcphosting.Tool{}, reader...), mcphosting.Tool{Name: "write", Description: "writer tool", InputSchema: map[string]any{"type": "object"}})
	p.PrimaryTools.Store(writer)
	p.RoleViews.Store(map[string][]mcphosting.Tool{"readiness-reader-token": reader, "readiness-writer-token": writer})
	roles := []api.MCPReadinessRole{}
	for _, entry := range []struct {
		name, token string
		tools       []mcphosting.Tool
	}{{"reader", "readiness-reader-token", reader}, {"writer", "readiness-writer-token", writer}} {
		key := "MCP_READINESS_" + strings.ToUpper(entry.name)
		cipher, err := secretbox.SealOne(p.Identity.Recipient(), key, entry.token, api.MCPCatalogOAuthMaxTokenBytes)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Base.Store.UpsertAppSecretWithKidInScope(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, api.DefaultEnvScope, key, p.Identity.Recipient().String(), cipher); err != nil {
			t.Fatal(err)
		}
		contract, err := mcphosting.NewCatalogContract(version, mcphosting.Catalog{Tools: entry.tools})
		if err != nil {
			t.Fatal(err)
		}
		body, err := mcphosting.MarshalContract(contract)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(body)
		roles = append(roles, api.MCPReadinessRole{Name: entry.name, ProtocolVersion: version, CatalogFormat: 2, CatalogSHA256: hex.EncodeToString(hash[:]), Monitor: &api.MCPReadinessMonitorConfig{Enabled: false, IntervalSeconds: 60, FailureThreshold: 1, SecretRef: api.SecretRefPrefix + key}})
	}
	store := p.Base.Store.(state.MCPReadinessStore)
	policy, err := store.SetMCPReadinessPolicy(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Roles: roles, Reason: "require separate reader and writer views"})
	if err != nil {
		t.Fatal(err)
	}
	in := p.Publication.Intent
	in.Auth.Scopes = []string{"tool:read", "tool:write"}
	in.ToolScopes = map[string][]string{"visible": {"tool:read"}, "write": {"tool:write"}}
	in.Readiness = toolpublication.ReadinessReview(policy)
	id := strings.ReplaceAll(uuid.NewString(), "-", "") + strings.ReplaceAll(uuid.NewString(), "-", "")
	out, err := p.Publisher.BeginToolPublication(t.Context(), id, in)
	if err != nil {
		t.Fatal(err)
	}
	p.Publication = out.Record
	return policy
}
