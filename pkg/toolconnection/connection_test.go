package toolconnection_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolconnection"
)

func TestExecutionConnectionDigestIgnoresTokenRenewal(t *testing.T) {
	r := api.ToolOAuthConnectionRecord{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), Intent: api.ToolOAuthConnectionIntent{App: "private-tools", Scope: "default", Issuer: "https://provider.example", TokenEndpoint: "https://provider.example/token", ClientID: "client", Resource: "https://tool.example/mcp", Scopes: []string{"tool:read"}, RefreshTokenSecretRef: "secret:MCP_EXECUTION_REFRESH", TokenAuthMethod: "none"}, CreatedAt: time.Now(), SourceSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	r.BindingSHA256 = toolconnection.Digest(r)
	if err := toolconnection.ValidateRecord(r); err != nil {
		t.Fatal(err)
	}
	changed := r
	changed.State = "ready"
	changed.Generation = 42
	expiry := time.Now().Add(time.Hour)
	changed.ExpiresAt = &expiry
	if toolconnection.Digest(changed) != r.BindingSHA256 {
		t.Fatal("token renewal changes approval identity")
	}
	for _, mutate := range []func(*api.ToolOAuthConnectionRecord){func(x *api.ToolOAuthConnectionRecord) { x.AppID = uuid.NewString() }, func(x *api.ToolOAuthConnectionRecord) { x.Intent.Resource = "https://other.example/mcp" }, func(x *api.ToolOAuthConnectionRecord) { x.Intent.Scopes = []string{"tool:write"} }, func(x *api.ToolOAuthConnectionRecord) { x.Intent.TokenEndpoint = "https://other.example/token" }} {
		x := r
		mutate(&x)
		if toolconnection.ValidateRecord(x) == nil {
			t.Fatal("changed authorization preserved approval")
		}
	}
}

func TestExecutionConnectionLostLeaseRequiresReconnect(t *testing.T) {
	past := time.Now().Add(-time.Second)
	future := time.Now().Add(time.Hour)
	r := api.ToolOAuthConnectionRecord{State: "refreshing"}
	if out := toolconnection.View(r, &past, time.Now()); out.State != "reconnect_required" || out.RecoveryAction != "reconnect" {
		t.Fatal("expired lease is not fenced")
	}
	if out := toolconnection.View(r, &future, time.Now()); out.State != "refreshing" {
		t.Fatal("active lease was not retained")
	}
}

func TestExecutionConnectionRejectsInvalidIntent(t *testing.T) {
	for _, raw := range []string{"http://provider.example/token", "https://user:secret@provider.example/token", "https://provider.example/token?secret=value", "https://provider.example/token#part"} {
		if toolconnection.ValidURL(raw) {
			t.Fatal("unreviewed endpoint syntax accepted")
		}
	}
	for _, scopes := range [][]string{{}, {"tool:read", "tool:read"}, {"read write"}, {"\n"}} {
		if toolconnection.ValidScopes(scopes) {
			t.Fatal("invalid scope set accepted")
		}
	}
}
