// adr: 20261110210000
package secretd_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

func sidecarsJSON(t *testing.T, name string, env map[string][]byte) []byte {
	t.Helper()
	out := `[{"name":"` + name + `","env":{`
	first := true
	for k, v := range env {
		if !first {
			out += ","
		}
		first = false
		out += `"` + k + `":"` + base64.StdEncoding.EncodeToString(v) + `"`
	}
	return []byte(out + `}}]`)
}

func TestSidecarCiphertextsAppliesMatchingReseals(t *testing.T) {
	declared := map[string][]byte{"A": []byte("ct-a"), "B": []byte("ct-b")}
	raw := sidecarsJSON(t, "proxy", declared)
	sum := sha256.Sum256([]byte("ct-a"))
	reseals := []secretd.SidecarEnvReseal{
		{SidecarName: "proxy", Key: "A", SourceSHA256: hex.EncodeToString(sum[:]), Ciphertext: []byte("new-a")},
		{SidecarName: "proxy", Key: "B", SourceSHA256: "stale", Ciphertext: []byte("new-b")},
		{SidecarName: "other", Key: "A", SourceSHA256: hex.EncodeToString(sum[:]), Ciphertext: []byte("wrong")},
	}
	got, err := secretd.SidecarCiphertexts(raw, "proxy", reseals)
	if err != nil || string(got["A"]) != "new-a" || string(got["B"]) != "ct-b" || len(got) != 2 {
		t.Fatalf("ciphertexts = %q, %v", got, err)
	}
	if _, err := secretd.SidecarCiphertexts(raw, "missing", nil); !errors.Is(err, secretd.ErrUnknownSidecar) {
		t.Fatalf("missing sidecar = %v", err)
	}
	if _, err := secretd.SidecarCiphertexts(nil, "proxy", nil); !errors.Is(err, secretd.ErrUnknownSidecar) {
		t.Fatalf("no sidecars = %v", err)
	}
}

func TestOpenSidecarEnvByReference(t *testing.T) {
	f := newFixture(t, 0)
	seal := func(namespace, value string) []byte {
		ct, err := secretbox.SealBytesTo([]*age.X25519Recipient{f.id.Recipient()}, namespace, []byte(value), 0)
		if err != nil {
			t.Fatal(err)
		}
		return ct
	}
	f.store.deployments = map[string]fakeDeployment{
		"dep-a": {app: "app-a", sidecars: sidecarsJSON(t, "proxy", map[string][]byte{"TOKEN": seal(secretd.SidecarEnvNamespace, "s3cret")})},
		"dep-b": {app: "app-b", sidecars: sidecarsJSON(t, "proxy", map[string][]byte{"TOKEN": seal(secretd.SidecarEnvNamespace, "theirs")})},
		"dep-x": {app: "app-a", sidecars: sidecarsJSON(t, "proxy", map[string][]byte{"TOKEN": seal("app_env", "wrong namespace")})},
	}
	open := func(app, dep string) (map[string]string, codes.Code) {
		resp, err := f.server.OpenSidecarEnv(f.ctx, &secretdpb.OpenSidecarEnvRequest{AppId: app, DeploymentId: dep, SidecarName: "proxy"})
		if err != nil {
			return nil, status.Code(err)
		}
		return resp.GetValues(), codes.OK
	}
	if got, code := open("app-a", "dep-a"); code != codes.OK || got["TOKEN"] != "s3cret" {
		t.Fatalf("owner open = %v, %v", got, code)
	}
	// The caller owns app-a and names app-b's deployment under it.
	if _, code := open("app-a", "dep-b"); code != codes.NotFound {
		t.Fatalf("another app's deployment = %v", code)
	}
	// The caller does not own app-b.
	if _, code := open("app-b", "dep-b"); code != codes.PermissionDenied {
		t.Fatalf("not the owner = %v", code)
	}
	if _, code := open("app-a", "dep-x"); code != codes.FailedPrecondition {
		t.Fatalf("wrong namespace = %v", code)
	}
}
