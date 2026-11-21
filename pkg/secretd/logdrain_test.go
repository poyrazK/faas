package secretd_test

import (
	"testing"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

// Slice 3f: a log drain header opens by reference with the edge identity,
// for a drain of the app, and only for its owner or a granted peer.
func TestOpenLogDrainHeaderByReference(t *testing.T) {
	f := newFixture(t, 0)
	edge, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(namespace, value string) []byte {
		t.Helper()
		blob, err := secretbox.SealBytes(edge.Recipient(), namespace, []byte(value), secretd.MaxLogDrainHeaderBytes)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	f.store.drains = map[[2]string][]byte{
		{"app-a", "drain-1"}: seal(secretd.LogDrainHeaderNamespace, "Bearer drain-token"),
		{"app-a", "drain-2"}: seal("SOMETHING_ELSE", "Bearer other"),
		{"app-b", "drain-3"}: seal(secretd.LogDrainHeaderNamespace, "Bearer b-token"),
	}
	open := func(app, drain string) (string, codes.Code) {
		resp, err := f.server.OpenLogDrainHeader(f.ctx, &secretdpb.OpenLogDrainHeaderRequest{AppId: app, DrainId: drain})
		return resp.GetHeader(), status.Code(err)
	}
	if _, code := open("app-a", "drain-1"); code != codes.FailedPrecondition {
		t.Fatalf("without an edge identity = %v, want FailedPrecondition", code)
	}
	f.server.WithEdgeIdentities([]*age.X25519Identity{edge})
	if header, code := open("app-a", "drain-1"); code != codes.OK || header != "Bearer drain-token" {
		t.Fatalf("owner = %q, %v", header, code)
	}
	if f.store.opens[len(f.store.opens)-1] != "n-owner/app-a/owner" {
		t.Fatalf("audit = %v", f.store.opens)
	}
	for _, tc := range []struct {
		name, app, drain string
		want             codes.Code
	}{
		{"another app's drain", "app-a", "drain-3", codes.NotFound},
		{"another namespace", "app-a", "drain-2", codes.FailedPrecondition},
		{"another node's app", "app-b", "drain-3", codes.PermissionDenied},
	} {
		if _, code := open(tc.app, tc.drain); code != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, code, tc.want)
		}
	}
	if len(f.store.opens) != 1 {
		t.Fatalf("refused opens were recorded: %v", f.store.opens)
	}
}
