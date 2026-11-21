package secretd_test

import (
	"testing"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

// Slice 3i: an edge-rule header value opens by reference with the edge
// identity, for the node that runs the target app, from an app of the same
// account whose rule references it.
func TestOpenEdgeHeaderSecretByReference(t *testing.T) {
	f := newFixture(t, 0)
	f.store.apps["app-a2"] = secretd.AppTarget{OwnerNodeID: "n-other", AccountID: "acct-a"}
	edge, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(namespace, value string) []byte {
		t.Helper()
		blob, err := secretbox.SealBytes(edge.Recipient(), namespace, []byte(value), api.EdgeRuleHeaderSecretMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	f.store.edgeHeaders = map[[2]string][]byte{
		{"app-a", "s-1"}:  seal(api.EdgeRuleHeaderSecretSealNamespace, "Bearer upstream"),
		{"app-a", "s-2"}:  seal(secretd.LogDrainHeaderNamespace, "Bearer drain"),
		{"app-a2", "s-3"}: seal(api.EdgeRuleHeaderSecretSealNamespace, "Bearer sibling"),
		{"app-b", "s-4"}:  seal(api.EdgeRuleHeaderSecretSealNamespace, "Bearer other-tenant"),
	}
	open := func(target, app, id string) (string, codes.Code) {
		resp, err := f.server.OpenEdgeHeaderSecret(f.ctx, &secretdpb.OpenEdgeHeaderSecretRequest{
			TargetAppId: target, SecretAppId: app, SecretId: id})
		return resp.GetValue(), status.Code(err)
	}
	if _, code := open("app-a", "app-a", "s-1"); code != codes.FailedPrecondition {
		t.Fatalf("without an edge identity = %v, want FailedPrecondition", code)
	}
	f.server.WithEdgeIdentities([]*age.X25519Identity{edge})
	if v, code := open("app-a", "app-a", "s-1"); code != codes.OK || v != "Bearer upstream" {
		t.Fatalf("owner = %q, %v", v, code)
	}
	if f.store.opens[len(f.store.opens)-1] != "n-owner/app-a/owner" {
		t.Fatalf("audit = %v", f.store.opens)
	}
	// A route rule on app-a2's host sends traffic to app-a: the owner of
	// app-a opens app-a2's value, and the audit names app-a2.
	if v, code := open("app-a", "app-a2", "s-3"); code != codes.OK || v != "Bearer sibling" {
		t.Fatalf("same-account sibling = %q, %v", v, code)
	}
	if f.store.opens[len(f.store.opens)-1] != "n-owner/app-a2/owner" {
		t.Fatalf("audit = %v", f.store.opens)
	}
	for _, tc := range []struct {
		name, target, app, id string
		want                  codes.Code
	}{
		{"missing ids", "app-a", "", "s-1", codes.InvalidArgument},
		{"another namespace", "app-a", "app-a", "s-2", codes.FailedPrecondition},
		{"unreferenced or unknown value", "app-a", "app-a", "s-9", codes.NotFound},
		{"value of another app under the wrong app", "app-a", "app-a", "s-3", codes.NotFound},
		{"another account's value", "app-a", "app-b", "s-4", codes.PermissionDenied},
		{"an app the caller does not run", "app-b", "app-b", "s-4", codes.PermissionDenied},
		{"unknown secret app", "app-a", "app-x", "s-4", codes.NotFound},
	} {
		if _, code := open(tc.target, tc.app, tc.id); code != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, code, tc.want)
		}
	}
	// A peer with a grant for app-a opens it as well.
	f.caller = "peer-node"
	if _, code := open("app-a", "app-a", "s-1"); code != codes.PermissionDenied {
		t.Fatalf("ungranted peer = %v", code)
	}
}
