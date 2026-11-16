package secretd_test

// adr: 20261110210000

import (
	"context"
	"crypto/ed25519"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/secretd"
)

// Slice 3k-2: secretd mints a workflow identity for an app the caller owns
// or holds a grant for, in that app's account, and binds the body's hash.
func TestMintWorkflowIdentity(t *testing.T) {
	const (
		app     = "11111111-1111-4111-8111-111111111111"
		account = "22222222-2222-4222-8222-222222222222"
		other   = "33333333-3333-4333-8333-333333333333"
	)
	f := newFixture(t, 0)
	f.store.apps[app] = secretd.AppTarget{OwnerNodeID: "n-owner", AccountID: account}
	f.store.apps[other] = secretd.AppTarget{OwnerNodeID: "n-other", AccountID: account}
	body := []byte(`{"a":1}`)
	req := func(appID, accountID, hash string) *secretdpb.MintWorkflowIdentityRequest {
		return &secretdpb.MintWorkflowIdentityRequest{
			AccountId: accountID, AppId: appID, RunId: "44444444-4444-4444-8444-444444444444", StepName: "send",
			Attempt: 1, AttemptToken: "55555555-5555-4555-8555-555555555555", IntegrationId: "int-1",
			Method: "POST", Path: "/v1/contacts", PathTemplate: "/v1/contacts", BodySha256: hash,
		}
	}
	mint := func(r *secretdpb.MintWorkflowIdentityRequest) (string, codes.Code) {
		resp, err := f.server.MintWorkflowIdentity(f.ctx, r)
		if err != nil {
			return "", status.Code(err)
		}
		return resp.GetToken(), codes.OK
	}
	hash := outbound.WorkflowBodySHA256(body)
	if _, code := mint(req(app, account, hash)); code != codes.FailedPrecondition {
		t.Fatalf("mint without a key = %v", code)
	}
	priv, pub, err := internalsvc.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	f.server.WithInternalServiceKey(func(context.Context) (ed25519.PrivateKey, string, error) {
		return priv, internalsvc.KidFromPub(pub), nil
	})
	token, code := mint(req(app, account, hash))
	if code != codes.OK || token == "" {
		t.Fatalf("owner mint = %v", code)
	}
	// The same claims minted locally verify the same way: secretd binds the
	// body's hash exactly as MintWorkflowIdentity would.
	if internalsvc.StringClaim(token, "body_sha256") != hash || internalsvc.StringClaim(token, "app_id") != app {
		t.Fatalf("claims body_sha256=%q app_id=%q", internalsvc.StringClaim(token, "body_sha256"), internalsvc.StringClaim(token, "app_id"))
	}
	for name, tc := range map[string]struct {
		r    *secretdpb.MintWorkflowIdentityRequest
		want codes.Code
	}{
		"another node's app": {req(other, account, hash), codes.PermissionDenied},
		"wrong account":      {req(app, other, hash), codes.PermissionDenied},
		"no account":         {req(app, "", hash), codes.InvalidArgument},
		"bad body hash":      {req(app, account, "not-a-hash"), codes.InvalidArgument},
	} {
		if _, code := mint(tc.r); code != tc.want {
			t.Errorf("%s = %v, want %v", name, code, tc.want)
		}
	}
}
