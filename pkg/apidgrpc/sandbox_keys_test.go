package apidgrpc_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

type sandboxKeysServer struct {
	apidpb.UnimplementedAuthServer
	key sandboxtoken.PublicKey
}

func (s *sandboxKeysServer) SandboxTokenKeys(context.Context, *apidpb.SandboxTokenKeysRequest) (*apidpb.SandboxTokenKeysResponse, error) {
	return &apidpb.SandboxTokenKeysResponse{Keys: []*apidpb.SandboxTokenKey{{Id: s.key.ID, PublicKey: s.key.Key}}}, nil
}

// adr: 20261027090013 — the gateway keeps apid's sandbox token keys and
// verifies apid's tokens with them.
func TestRefreshSandboxKeysLoadsApidsKey(t *testing.T) {
	issuer, err := sandboxtoken.NewIssuer(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	client := dialSessionAuth(t, &sandboxKeysServer{key: issuer.PublicKey()})
	v := sandboxtoken.NewVerifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go apidgrpc.RefreshSandboxKeys(ctx, client, v, nil)
	deadline := time.Now().Add(5 * time.Second)
	for !v.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("the keys never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	now := time.Now()
	token, _ := issuer.Sign(sandboxtoken.Claims{SandboxID: "sb-1", AccountID: "acct-1", ExpiresAt: now.Add(time.Minute)})
	if _, err := v.Verify(token, now); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
