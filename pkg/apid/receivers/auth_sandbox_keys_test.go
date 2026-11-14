package receivers

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
	"github.com/onebox-faas/faas/pkg/session"
)

// adr: 20261027090013 — a gateway verifies sandbox tokens with the public key
// apid serves, derived from apid's session key; the key verifies apid's
// tokens and mints nothing (ADR-20261102191500).
func TestAuthReceiverSandboxTokenKeys(t *testing.T) {
	apidKey := make([]byte, 32)
	for i := range apidKey {
		apidKey[i] = byte(i + 7)
	}
	apid, err := session.NewManager(apidKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := dialAuthBufconnWith(t, &fakeAuthStore{}, nil, apid).SandboxTokenKeys(t.Context(), &apidpb.SandboxTokenKeysRequest{})
	if err != nil || len(resp.GetKeys()) != 1 {
		t.Fatalf("keys = %+v, %v", resp, err)
	}
	issuer, err := sandboxtoken.NewIssuer(apid.BindingKey())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	token, err := issuer.Sign(sandboxtoken.Claims{SandboxID: "sb-1", AccountID: "acct-1", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	k := resp.GetKeys()[0]
	if _, err := sandboxtoken.NewVerifier(sandboxtoken.PublicKey{ID: k.GetId(), Key: k.GetPublicKey()}).Verify(token, now); err != nil {
		t.Fatalf("the served key does not verify apid's token: %v", err)
	}
	if _, err := dialAuthBufconnWith(t, &fakeAuthStore{}, nil, nil).SandboxTokenKeys(t.Context(), &apidpb.SandboxTokenKeysRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("without a session key: %v, want Unavailable", err)
	}
}
