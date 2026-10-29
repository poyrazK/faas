package livequeries_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// apid signs each live key and login it seals for the row it writes them to
// (ADR 20261028161502), so livequeryd can tell them from values a database
// writer stored.
func TestLiveQueriesSignWhatTheySeal(t *testing.T) {
	h := newHarness(t)
	h.svc.Log = slog.New(slog.DiscardHandler)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Signer = livekeys.NewSigner(private)
	verify := livekeys.NewVerifier([]ed25519.PublicKey{public}, true)
	material := managedpostgres.CredentialMaterial{ProviderIdentityID: "gregale_live_ab", Username: "gregale_live_ab", Password: "pw",
		Database: "neondb", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: "ep-1.neon.tech", Port: 5432}}}
	h.db.issuer = func(string) (managedpostgres.LiveLogin, error) {
		return managedpostgres.LiveLogin{CredentialMaterial: material}, nil
	}
	if got := h.do(t, http.MethodPost, "/", "", h.svc.Enable); got.status != http.StatusCreated {
		t.Fatalf("enable = %d %s", got.status, got.code)
	}
	if len(h.keys.sealed) != 1 {
		t.Fatalf("stored %d keys", len(h.keys.sealed))
	}
	for kid, key := range h.keys.sealed {
		if err := verify.Check(livekeys.SignatureKindKey, "db-1", kid, key.Recipient, key.Sealed, key.Signature); err != nil {
			t.Fatalf("the stored key's signature = %v", err)
		}
	}
	login := h.keys.login
	if login == nil {
		t.Fatal("no login stored")
	}
	if err := verify.Check(livekeys.SignatureKindLogin, "db-1", "", login.Recipient, login.Sealed, login.Signature); err != nil {
		t.Fatalf("the stored login's signature = %v", err)
	}
}
