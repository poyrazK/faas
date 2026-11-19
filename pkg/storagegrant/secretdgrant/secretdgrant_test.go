package secretdgrant

// adr: 20261117190000

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagegrant"
)

type fakeRPC struct {
	secretdpb.SecretdClient
	got  *secretdpb.GrantStorageObjectsRequest
	resp *secretdpb.GrantStorageObjectsResponse
}

func (f *fakeRPC) GrantStorageObjects(_ context.Context, in *secretdpb.GrantStorageObjectsRequest, _ ...grpc.CallOption) (*secretdpb.GrantStorageObjectsResponse, error) {
	f.got = in
	return f.resp, nil
}

func TestClientMapsRequestsAndGrants(t *testing.T) {
	exp := time.Date(2026, 11, 18, 12, 5, 0, 0, time.UTC)
	rpc := &fakeRPC{resp: &secretdpb.GrantStorageObjectsResponse{Grants: []*secretdpb.StorageObjectGrant{
		{Key: "base/a", Method: "GET", Url: "https://s/a", ExpiresAt: exp.Format(time.RFC3339)},
	}}}
	got, err := Client{RPC: rpc}.GrantObjects(t.Context(), []storagegrant.Request{{Key: "base/a", Op: storagegrant.OpGet}})
	if err != nil || len(got) != 1 || got[0].URL != "https://s/a" || !got[0].Expires.Equal(exp) {
		t.Fatalf("grants = %+v, %v", got, err)
	}
	if rpc.got.GetObjects()[0].GetOp() != "get" {
		t.Fatalf("sent %+v", rpc.got)
	}
	// A grant for another key than asked is refused.
	rpc.resp.Grants[0].Key = "base/b"
	if _, err := (Client{RPC: rpc}).GrantObjects(t.Context(), []storagegrant.Request{{Key: "base/a", Op: storagegrant.OpGet}}); err == nil {
		t.Fatal("accepted a grant for another key")
	}
}

func TestFromEnv(t *testing.T) {
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if on, _, err := FromEnv(t.Context(), env(nil), local, "test", nil); on || err != nil {
		t.Fatalf("default = %v, %v; want direct", on, err)
	}
	if _, _, err := FromEnv(t.Context(), env(map[string]string{AccessEnv: "bogus"}), local, "test", nil); err == nil {
		t.Fatal("accepted an unknown mode")
	}
	if _, _, err := FromEnv(t.Context(), env(map[string]string{AccessEnv: "secretd"}), local, "test", nil); err == nil {
		t.Fatal("secretd mode without a target")
	}
}
