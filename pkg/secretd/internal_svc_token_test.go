package secretd_test

// adr: 20261110210000

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/internalsvc"
)

// Slice 3k: secretd mints an internal-service token for the named app only,
// to its owner or a granted peer; without a key it refuses.
func TestMintInternalServiceToken(t *testing.T) {
	f := newFixture(t, 0)
	mint := func(app string) (string, codes.Code) {
		resp, err := f.server.MintInternalServiceToken(f.ctx, &secretdpb.MintInternalServiceTokenRequest{AppId: app})
		if err != nil {
			return "", status.Code(err)
		}
		return resp.GetToken(), codes.OK
	}
	if _, code := mint("app-a"); code != codes.FailedPrecondition {
		t.Fatalf("mint without a key = %v, want FailedPrecondition", code)
	}

	priv, pub, err := internalsvc.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	f.server.WithInternalServiceKey(func(context.Context) (ed25519.PrivateKey, string, error) {
		loads++
		return priv, internalsvc.KidFromPub(pub), nil
	})
	token, code := mint("app-a")
	if code != codes.OK {
		t.Fatalf("owner mint = %v", code)
	}
	if svc, err := internalsvc.Verify(token, map[string]ed25519.PublicKey{"schedd": pub}); err != nil || svc != "schedd" {
		t.Fatalf("minted token verifies = %q, %v", svc, err)
	}
	if got := internalsvc.StringClaim(token, "app_id"); got != "app-a" {
		t.Fatalf("app_id claim = %q, want app-a", got)
	}
	if _, code := mint("app-b"); code != codes.PermissionDenied {
		t.Fatalf("mint for another node's app = %v, want PermissionDenied", code)
	}
	if _, code := mint(""); code != codes.InvalidArgument {
		t.Fatalf("mint without an app = %v", code)
	}
	if _, code := mint("app-a"); code != codes.OK || loads != 1 {
		t.Fatalf("second mint = %v with %d key loads, want the cached key", code, loads)
	}
	f.caller = "peer-node"
	if _, code := mint("app-a"); code != codes.PermissionDenied {
		t.Fatalf("peer without a grant = %v, want PermissionDenied", code)
	}
}

// A failed reload keeps the key secretd had; a first load that fails
// refuses.
func TestMintInternalServiceTokenKeyReload(t *testing.T) {
	f := newFixture(t, 0)
	priv, pub, err := internalsvc.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	fail := true
	f.server.WithInternalServiceKey(func(context.Context) (ed25519.PrivateKey, string, error) {
		if fail {
			return nil, "", errors.New("row unsealable")
		}
		return priv, internalsvc.KidFromPub(pub), nil
	})
	req := &secretdpb.MintInternalServiceTokenRequest{AppId: "app-a"}
	if _, err := f.server.MintInternalServiceToken(f.ctx, req); status.Code(err) != codes.Unavailable {
		t.Fatalf("mint with no loadable key = %v, want Unavailable", err)
	}
	fail = false
	if _, err := f.server.MintInternalServiceToken(f.ctx, req); err != nil {
		t.Fatal(err)
	}
	fail = true
	f.now = f.now.Add(time.Hour)
	if _, err := f.server.MintInternalServiceToken(f.ctx, req); err != nil {
		t.Fatalf("mint after a failed reload = %v, want the kept key", err)
	}
}
