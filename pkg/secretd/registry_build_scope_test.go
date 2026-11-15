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

// Slice 3j-4: a build pulls only the repositories its deployment's
// Dockerfiles name. Observe counts and lets everything through; enforce
// refuses a repository outside the scope and every pull of a build whose
// deployment recorded none.
func TestRegistryFetchBuildScope(t *testing.T) {
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
	running := func(recorded bool, scope ...string) secretd.BuildRun {
		return secretd.BuildRun{AppID: "app-a", Running: true, StartedAt: f.now.Add(-time.Minute), Node: "peer-node",
			ScopeRecorded: recorded, Scope: scope}
	}
	f.store.builds = map[string]secretd.BuildRun{
		"b-scoped":     running(true, reg.host()+"/team/app"),
		"b-empty":      running(true),
		"b-unrecorded": running(false),
	}
	f.server.WithRegistryHTTPClient(reg.srv.Client()).WithBuildIdentities([]*age.X25519Identity{build})
	f.caller = "peer-node"
	fetch := func(buildID, path string) codes.Code {
		req := &secretdpb.RegistryFetchRequest{BuildId: buildID, Method: "GET", Url: reg.srv.URL + path}
		return status.Code(f.server.RegistryFetch(req, &fetchStream{ctx: f.ctx}))
	}
	const inScope, outOfScope, outBlob = "/v2/team/app/manifests/v1", "/v2/team/other/manifests/v1", "/v2/team/other/blobs/sha256:abc"

	for _, tc := range []struct {
		mode  secretd.BuildRegistryScopeMode
		build string
		path  string
		want  codes.Code
	}{
		{secretd.BuildRegistryScopeEnforce, "b-scoped", inScope, codes.OK},
		{secretd.BuildRegistryScopeEnforce, "b-scoped", outOfScope, codes.PermissionDenied},
		{secretd.BuildRegistryScopeEnforce, "b-scoped", outBlob, codes.PermissionDenied},
		{secretd.BuildRegistryScopeEnforce, "b-scoped", "/v2/_catalog", codes.PermissionDenied},
		{secretd.BuildRegistryScopeEnforce, "b-empty", inScope, codes.PermissionDenied},
		{secretd.BuildRegistryScopeEnforce, "b-unrecorded", inScope, codes.PermissionDenied},
		{secretd.BuildRegistryScopeObserve, "b-scoped", outOfScope, codes.OK},
		{secretd.BuildRegistryScopeObserve, "b-unrecorded", inScope, codes.OK},
		{secretd.BuildRegistryScopeOff, "b-unrecorded", outOfScope, codes.OK},
	} {
		f.server.WithBuildRegistryScope(tc.mode)
		if got := fetch(tc.build, tc.path); got != tc.want {
			t.Errorf("%s %s %s = %v, want %v", tc.mode, tc.build, tc.path, got, tc.want)
		}
	}
}

func TestParseBuildRegistryScopeMode(t *testing.T) {
	for in, want := range map[string]secretd.BuildRegistryScopeMode{
		"": secretd.BuildRegistryScopeObserve, "off": secretd.BuildRegistryScopeOff,
		"observe": secretd.BuildRegistryScopeObserve, "enforce": secretd.BuildRegistryScopeEnforce,
	} {
		if got, err := secretd.ParseBuildRegistryScopeMode(in); err != nil || got != want {
			t.Errorf("ParseBuildRegistryScopeMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := secretd.ParseBuildRegistryScopeMode("on"); err == nil {
		t.Error("ParseBuildRegistryScopeMode accepted on")
	}
}
