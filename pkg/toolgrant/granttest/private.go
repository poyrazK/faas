package granttest

import (
	"context"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/toolregistry"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type PrivateFixture struct {
	Base     *Fixture
	Runtime  *Runtime
	Identity *age.X25519Identity
	Grant    api.ToolGrantRecord
	Token    string
}

func NewPrivate(t *testing.T, store state.Store, agentToken string) *PrivateFixture {
	t.Helper()
	f := New(t, store, agentToken, true)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	p := &PrivateFixture{Base: f, Identity: identity, Token: "provider-private-tool-access-token"}
	p.Rotate(t, p.Token)
	p.Runtime = NewRuntime(t, f, p.Token)
	f.PrivateExecutor = toolregistry.NewPrivateExecutor(store, toolregistry.PrivateExecutionOptions{Enabled: true, Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }, Endpoint: func(context.Context, state.App, state.Deployment) (string, error) { return f.PreviewURL, nil }, VerifyPin: func(context.Context, api.ToolInvocationTarget, string) error { return nil }, HTTP: p.Runtime.Server.Client()})
	t.Cleanup(f.PrivateExecutor.Close)
	p.Grant, err = f.Owner.PutToolGrant(t.Context(), uuid.NewString(), api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", AllowInvocation: true, ExpiresAt: time.Now().UTC().Add(time.Hour), CredentialSecretRef: "secret:MCP_TOOL_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (p *PrivateFixture) Rotate(t *testing.T, token string) {
	t.Helper()
	ciphertext, err := secretbox.SealOne(p.Identity.Recipient(), "MCP_TOOL_TOKEN", token, api.MCPCatalogOAuthMaxTokenBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Base.Store.UpsertAppSecretWithKidInScope(t.Context(), p.Base.Account.ID, p.Base.AppA.ID, api.DefaultEnvScope, "MCP_TOOL_TOKEN", p.Identity.Recipient().String(), ciphertext); err != nil {
		t.Fatal(err)
	}
}
