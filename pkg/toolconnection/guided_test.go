package toolconnection_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolconnection"
)

func TestGuidedExecutionAuthorityHasNoSourceSecrets(t *testing.T) {
	in := api.ToolOAuthConnectionIntent{Guided: true, App: "reports", Scope: "default", Issuer: "https://identity.example", AuthorizationEndpoint: "https://identity.example/authorize", TokenEndpoint: "https://identity.example/token", ClientID: "public-client", Resource: "https://reports.example/mcp", Scopes: []string{"reports:read"}, TokenAuthMethod: "none"}
	r := api.ToolOAuthConnectionRecord{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), Intent: in, CreatedAt: time.Now(), SourceSHA256: toolconnection.GuidedSourceDigest()}
	r.BindingSHA256 = toolconnection.Digest(r)
	if err := toolconnection.ValidateRecord(r); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*api.ToolOAuthConnectionIntent){func(x *api.ToolOAuthConnectionIntent) { x.RefreshTokenSecretRef = "secret:MCP_REFRESH" }, func(x *api.ToolOAuthConnectionIntent) { x.ClientSecretRef = "secret:MCP_CLIENT" }, func(x *api.ToolOAuthConnectionIntent) { x.TokenAuthMethod = "client_secret_basic" }, func(x *api.ToolOAuthConnectionIntent) { x.AuthorizationEndpoint = "http://identity.example/authorize" }, func(x *api.ToolOAuthConnectionIntent) { x.Guided = false }} {
		x := in
		mutate(&x)
		if toolconnection.ValidateIntent(x) == nil {
			t.Fatal("guided source restrictions bypassed")
		}
	}
	changed := r
	changed.Intent.AuthorizationEndpoint = "https://identity.example/other"
	if toolconnection.ValidateRecord(changed) == nil {
		t.Fatal("authorization endpoint change retained approval")
	}
}

func TestGuidedExecutionCallbackIsExactLoopback(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"http://127.0.0.1:34567/callback", true}, {"http://localhost:34567/callback", false}, {"https://127.0.0.1:34567/callback", false}, {"http://127.0.0.1:0/callback", false}, {"http://127.0.0.1:99999/callback", false}, {"http://127.0.0.1:034567/callback", false}, {"http://user@127.0.0.1:34567/callback", false}, {"http://127.0.0.1:34567/callback?code=x", false}, {"http://127.0.0.1:34567/call%62ack", false}, {"http://127.0.0.1:34567/callback#fragment", false},
	} {
		if toolconnection.ValidCallback(tc.url) != tc.valid {
			t.Fatalf("callback binding: %s", tc.url)
		}
	}
}
