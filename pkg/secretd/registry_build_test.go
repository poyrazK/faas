package secretd_test

import (
	"testing"
	"time"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

// Slice 3j-3: a builder VM's mirror pulls with the build's app credential
// for the node that claimed the running build, within the build's window,
// and the open is audited under the app. Any other node, a finished or stale
// build, or a build with no recorded node is refused.
func TestRegistryFetchForABuild(t *testing.T) {
	reg := newFakeRegistry(t, false)
	f := newFixture(t, 0)
	build, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(build.Recipient(), secretd.RegistryCredentialNamespace, []byte("s3cret"), 0)
	if err != nil {
		t.Fatal(err)
	}
	f.store.registries = map[[2]string]fakeRegistryCredential{{"app-a", reg.host()}: {username: "robot", sealed: sealed}}
	f.store.builds = map[string]secretd.BuildRun{
		"b-running":  {AppID: "app-a", Running: true, StartedAt: f.now.Add(-time.Minute), Node: "peer-node"},
		"b-finished": {AppID: "app-a", Running: false, StartedAt: f.now.Add(-time.Minute), Node: ""},
		"b-stale":    {AppID: "app-a", Running: true, StartedAt: f.now.Add(-2 * time.Hour), Node: "peer-node"},
		"b-nobody":   {AppID: "app-a", Running: true, StartedAt: f.now.Add(-time.Minute), Node: ""},
		"b-no-creds": {AppID: "app-b", Running: true, StartedAt: f.now.Add(-time.Minute), Node: "peer-node"},
	}
	f.server.WithRegistryHTTPClient(reg.srv.Client()).WithBuildIdentities([]*age.X25519Identity{build})
	manifest := reg.srv.URL + "/v2/team/app/manifests/v1"
	fetch := func(req *secretdpb.RegistryFetchRequest) codes.Code {
		return status.Code(f.server.RegistryFetch(req, &fetchStream{ctx: f.ctx}))
	}
	byBuild := func(id string) *secretdpb.RegistryFetchRequest {
		return &secretdpb.RegistryFetchRequest{BuildId: id, Method: "GET", Url: manifest}
	}

	// peer-node neither owns app-a nor holds a grant: the claim alone admits it.
	f.caller = "peer-node"
	if code := fetch(byBuild("b-running")); code != codes.OK {
		t.Fatalf("the build's node = %v", code)
	}
	if len(f.store.opens) != 1 || f.store.opens[0] != "n-peer/app-a/grant" {
		t.Fatalf("build audit = %v", f.store.opens)
	}
	for name, tc := range map[string]struct {
		req  *secretdpb.RegistryFetchRequest
		want codes.Code
	}{
		"finished build":         {byBuild("b-finished"), codes.PermissionDenied},
		"past the build window":  {byBuild("b-stale"), codes.PermissionDenied},
		"no node recorded":       {byBuild("b-nobody"), codes.PermissionDenied},
		"unknown build":          {byBuild("b-missing"), codes.NotFound},
		"app without credential": {byBuild("b-no-creds"), codes.NotFound},
		"build and app":          {&secretdpb.RegistryFetchRequest{BuildId: "b-running", AppId: "app-a", Method: "GET", Url: manifest}, codes.InvalidArgument},
		"build and job":          {&secretdpb.RegistryFetchRequest{BuildId: "b-running", JobId: "job-1", Method: "GET", Url: manifest}, codes.InvalidArgument},
		"not a GET or HEAD":      {&secretdpb.RegistryFetchRequest{BuildId: "b-running", Method: "PUT", Url: manifest}, codes.InvalidArgument},
	} {
		if code := fetch(tc.req); code != tc.want {
			t.Errorf("%s = %v, want %v", name, code, tc.want)
		}
	}
	// The owner of app-a is not the build's node: it is refused by build id.
	f.caller = "owner-node"
	if code := fetch(byBuild("b-running")); code != codes.PermissionDenied {
		t.Fatalf("a node other than the build's = %v", code)
	}
}
