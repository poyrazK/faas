package toolgrant_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestPrivateCredentialBinding(t *testing.T) {
	now := time.Now().UTC()
	g := api.ToolGrantRecord{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), App: "private-tools", CreatedAt: now, Intent: api.ToolGrantIntent{KeyID: uuid.NewString(), Version: toolpublication.Digest([]byte("version")), Name: "read", ExpiresAt: now.Add(time.Hour), AllowInvocation: true, CredentialSecretRef: "secret:MCP_ACCESS_TOKEN"}, Credential: &api.ToolGrantCredential{SourceSHA256: toolpublication.Digest([]byte("sealed source")), Scope: api.DefaultEnvScope, RequiredScopes: []string{"read"}}}
	g.Credential.SHA256 = toolgrant.CredentialDigest(g)
	if toolgrant.ValidateRecord(g) != nil || !toolgrant.CredentialMatches(g, "external-oauth", g.Credential.SHA256) || toolgrant.CredentialMatches(g, "open", "") || toolgrant.Active(g, now.Add(2*time.Hour)) {
		t.Fatal("private grant binding invalid")
	}
	for _, mutate := range []func(*api.ToolGrantRecord){func(g *api.ToolGrantRecord) { g.ID = uuid.NewString() }, func(g *api.ToolGrantRecord) { g.AppID = uuid.NewString() }, func(g *api.ToolGrantRecord) { g.Intent.KeyID = uuid.NewString() }, func(g *api.ToolGrantRecord) { g.Intent.Name = "sibling" }, func(g *api.ToolGrantRecord) { g.Intent.CredentialSecretRef = "secret:OTHER_TOKEN" }} {
		changed := g
		copy := *g.Credential
		changed.Credential = &copy
		mutate(&changed)
		if toolgrant.ValidateRecord(changed) == nil {
			t.Fatal("changed private identity retained authority")
		}
	}
}
