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

// Slice 3j-2b: a job's private image is pulled with the job's credential for
// the node holding its live materialization lease, and audited under the job.
func TestRegistryFetchForAJob(t *testing.T) {
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
	f.store.jobCreds = map[[2]string]fakeRegistryCredential{{"job-1", reg.host()}: {username: "robot", sealed: sealed}}
	f.store.jobLeases = map[string]fakeJobLease{
		"job-1": {owner: "owner-node/claim-1", until: f.now.Add(time.Minute)},
		"job-2": {owner: "owner-node/claim-2", until: f.now.Add(-time.Second)},
		"job-3": {owner: "owner-node-2/claim-3", until: f.now.Add(time.Minute)},
	}
	f.server.WithRegistryHTTPClient(reg.srv.Client()).WithBuildIdentities([]*age.X25519Identity{build})
	manifest := reg.srv.URL + "/v2/team/app/manifests/v1"
	fetch := func(req *secretdpb.RegistryFetchRequest) codes.Code {
		return status.Code(f.server.RegistryFetch(req, &fetchStream{ctx: f.ctx}))
	}
	job := func(id string) *secretdpb.RegistryFetchRequest {
		return &secretdpb.RegistryFetchRequest{JobId: id, Method: "GET", Url: manifest}
	}
	if code := fetch(job("job-1")); code != codes.OK {
		t.Fatalf("lease holder = %v", code)
	}
	if len(f.store.jobOpens) != 1 || f.store.jobOpens[0] != "n-owner/job-1/grant" {
		t.Fatalf("job audit = %v", f.store.jobOpens)
	}
	for name, tc := range map[string]struct {
		req  *secretdpb.RegistryFetchRequest
		want codes.Code
	}{
		"expired lease":            {job("job-2"), codes.PermissionDenied},
		"name prefix of a node":    {job("job-3"), codes.PermissionDenied},
		"unknown job":              {job("job-9"), codes.NotFound},
		"both app and job":         {&secretdpb.RegistryFetchRequest{AppId: "app-a", JobId: "job-1", Method: "GET", Url: manifest}, codes.InvalidArgument},
		"neither app nor job":      {&secretdpb.RegistryFetchRequest{Method: "GET", Url: manifest}, codes.InvalidArgument},
		"app credential for a job": {&secretdpb.RegistryFetchRequest{AppId: "app-a", Method: "GET", Url: manifest}, codes.NotFound},
	} {
		if code := fetch(tc.req); code != tc.want {
			t.Errorf("%s = %v, want %v", name, code, tc.want)
		}
	}
	f.caller = "peer-node"
	if code := fetch(job("job-1")); code != codes.PermissionDenied {
		t.Fatalf("another node = %v", code)
	}
}
