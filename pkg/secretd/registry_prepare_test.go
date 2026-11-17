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

// Slice 3j-2: the node preparing a deployment's image pulls the app's
// private registry even when it is not the app's owner; another node is
// refused.
func TestRegistryFetchAdmitsTheImagePreparer(t *testing.T) {
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
	// app-b is owned by n-other; the caller (owner-node) prepares its image.
	f.store.registries = map[[2]string]fakeRegistryCredential{{"app-b", reg.host()}: {username: "robot", sealed: sealed}}
	f.server.WithRegistryHTTPClient(reg.srv.Client()).WithBuildIdentities([]*age.X25519Identity{build})
	fetch := func() codes.Code {
		s := &fetchStream{ctx: f.ctx}
		return status.Code(f.server.RegistryFetch(&secretdpb.RegistryFetchRequest{AppId: "app-b", Method: "GET",
			Url: reg.srv.URL + "/v2/team/app/manifests/v1"}, s))
	}
	if code := fetch(); code != codes.PermissionDenied {
		t.Fatalf("without a preparation = %v", code)
	}
	f.store.preparing = map[[2]string]bool{{"app-b", "owner-node"}: true}
	if code := fetch(); code != codes.OK {
		t.Fatalf("preparing node = %v", code)
	}
	if got := f.store.opens[len(f.store.opens)-1]; got != "n-owner/app-b/grant" {
		t.Fatalf("audit = %s", got)
	}
	f.caller = "peer-node"
	if code := fetch(); code != codes.PermissionDenied {
		t.Fatalf("a node preparing nothing = %v", code)
	}
}
