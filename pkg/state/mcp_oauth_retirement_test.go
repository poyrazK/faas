package state

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestDurableRetirementExpiryAndUnknownIssuance(t *testing.T) {
	at := time.Now().UTC()
	phase := &api.MCPCatalogOAuthRevocation{ID: "record", State: "pending", NextAttemptAt: at, Ciphertext: []byte("sealed"), ClientCiphertext: []byte("client"), Config: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "client", RevocationEndpoint: "https://provider.example/revoke"}}
	r := newRetirement("app", "app", "reader", "disconnect", phase, at)
	job := retirementJob("account", r)
	if err := advanceRetirement(&r, &job, "claim", false, r.RetainUntil); err != nil {
		t.Fatal(err)
	}
	if r.State != "expired" || r.Revocation.Ciphertext != nil || r.Revocation.ClientCiphertext != nil || r.Revocation.Config != nil {
		t.Fatal("expiry retained sealed data")
	}
	phase = &api.MCPCatalogOAuthRevocation{ID: "unknown", State: "pending", NextAttemptAt: at, Config: &api.MCPCatalogObserverOAuthConfig{}}
	r = newRetirement("app", "app", "reader", "authorization_exchange", phase, at)
	r.State = "awaiting_issuance"
	job = retirementJob("account", r)
	if err := advanceRetirement(&r, &job, "claim", false, at); err != nil {
		t.Fatal(err)
	}
	if r.State != "manual_required" || r.Reason != "issuance_unknown" || r.Revocation.AccessAcknowledged || r.Revocation.RefreshAcknowledged {
		t.Fatal("unknown issuance claimed provider evidence")
	}
	if err := recoverRetirement(&r, api.MCPOAuthRetirementAction{Action: "retry"}, at); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("unknown tokens were retried", err)
	}
	if err := recoverRetirement(&r, api.MCPOAuthRetirementAction{Action: "acknowledge_manual"}, at); err != nil {
		t.Fatal(err)
	}
	if r.State != "manually_acknowledged" || r.Revocation.Config != nil {
		t.Fatal("manual acknowledgement retained private binding")
	}
}
func TestDurableRetirementHoldAndRecovery(t *testing.T) {
	at := time.Now().UTC()
	phase := &api.MCPCatalogOAuthRevocation{ID: "held", State: "pending", NextAttemptAt: at, Ciphertext: []byte("sealed"), Config: &api.MCPCatalogObserverOAuthConfig{RevocationEndpoint: "https://provider.example/revoke"}}
	r := newRetirement("app", "app", "reader", "replacement", phase, at)
	job := retirementJob("account", r)
	if err := advanceRetirement(&r, &job, "claim", true, at); err != nil {
		t.Fatal(err)
	}
	if r.State != "held" || r.Revocation.Attempts != 0 || r.Revocation.LeaseID != "" {
		t.Fatal("hold consumed retry budget or lease")
	}
	if err := recoverRetirement(&r, api.MCPOAuthRetirementAction{Action: "retry", AllowActiveGrantRevocation: true}, at); err != nil {
		t.Fatal(err)
	}
	job = retirementJob("account", r)
	if err := advanceRetirement(&r, &job, "claim", true, at); err != nil {
		t.Fatal(err)
	}
	if r.State != "pending" || r.Revocation.Attempts != 1 || r.Revocation.LeaseID == "" {
		t.Fatal("explicit recovery did not claim")
	}
	if err := recoverRetirement(&r, api.MCPOAuthRetirementAction{Action: "acknowledge_manual"}, at); !errors.Is(err, ErrConflict) {
		t.Fatal("live lease was overwritten", err)
	}
}
