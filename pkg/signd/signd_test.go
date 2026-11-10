package signd_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	signdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/signd/v1"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

const (
	depID = "11111111-1111-1111-1111-111111111111"
	node  = "fsn-compute-1"
)

type memObjects struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (m *memObjects) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs[key] = b
	return nil
}

func (m *memObjects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blobs, key)
	return nil
}

type fakeStore struct {
	claim   signd.Claim
	noClaim bool
	rows    map[string]signd.Record
	layers  []signd.StoredLayer
}

func (f *fakeStore) StoredLayers(context.Context) ([]signd.StoredLayer, error) {
	return f.layers, nil
}

func (f *fakeStore) BuildClaim(context.Context, string) (signd.Claim, error) {
	if f.noClaim {
		return signd.Claim{}, signd.ErrNoClaim
	}
	return f.claim, nil
}

func (f *fakeStore) RecordSignature(_ context.Context, r signd.Record) (signd.Record, bool, error) {
	if old, ok := f.rows[r.StorageKey]; ok {
		return old, false, nil
	}
	f.rows[r.StorageKey] = r
	return r, true, nil
}

// direct calls the server in process, as the named node.
type direct struct {
	srv  *signd.Server
	node string
}

func (d direct) SignArtifact(ctx context.Context, in *signdpb.SignArtifactRequest, _ ...grpc.CallOption) (*signdpb.SignArtifactResponse, error) {
	return d.srv.SignArtifact(peerContext(ctx, d.node), in)
}

// peerContext is what gRPC hands a handler for an mTLS caller whose
// verified leaf has CN cn.
func peerContext(ctx context.Context, cn string) context.Context {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	return peer.NewContext(ctx, &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}}})
}

type fixture struct {
	objects  *memObjects
	store    *fakeStore
	srv      *signd.Server
	pubPath  string
	priv     *ecdsa.PrivateKey
	privPath string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(t.TempDir(), "sign-pub.pem")
	if err := os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o400); err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		objects: &memObjects{blobs: map[string][]byte{}},
		store: &fakeStore{rows: map[string]signd.Record{}, claim: signd.Claim{Status: state.DeployImaging,
			Sidecars: []byte(`[{"name":"proxy","type":"sidecar","image":"x"}]`), AppSlug: "alice", NodeName: node, Phase: state.ImagePreparing}},
		pubPath: pubPath,
		priv:    priv,
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	f.privPath = filepath.Join(t.TempDir(), "sign.key")
	if err := os.WriteFile(f.privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o400); err != nil {
		t.Fatal(err)
	}
	f.srv = signd.NewServer(f.store, priv, f.objects, nil)
	return f
}

func (f *fixture) client(n string) *signd.Client {
	return signd.NewClient(direct{srv: f.srv, node: n}, f.objects, nil)
}

func (f *fixture) verify(t *testing.T, key string) error {
	t.Helper()
	v, err := cosign.NewLocalVerifier(f.pubPath, f.objects)
	if err != nil {
		t.Fatal(err)
	}
	return v.Verify(context.Background(), key, cosign.SigKeyFor(key))
}

func TestSignArtifact(t *testing.T) {
	appKey := storagekey.AppLayer("alice", depID)
	for _, tc := range []struct {
		name   string
		key    string
		caller string
		edit   func(*fakeStore)
		code   codes.Code
	}{
		{name: "app layer", key: appKey, caller: node},
		{name: "sidecar layer", key: storagekey.AppSidecarLayer("alice", depID, "proxy"), caller: node},
		{name: "another node", key: appKey, caller: "fsn-compute-2", code: codes.PermissionDenied},
		{name: "layer already published", key: appKey, caller: node, code: codes.PermissionDenied,
			edit: func(s *fakeStore) { s.claim.Phase = state.ImageLayerPublished }},
		{name: "deployment live", key: appKey, caller: node, code: codes.PermissionDenied,
			edit: func(s *fakeStore) { s.claim.Status = state.DeployLive }},
		{name: "deployment failed", key: appKey, caller: node, code: codes.PermissionDenied,
			edit: func(s *fakeStore) { s.claim.Status = state.DeployFailed }},
		{name: "another app's key", key: storagekey.AppLayer("bob", depID), caller: node, code: codes.PermissionDenied},
		{name: "unknown sidecar", key: storagekey.AppSidecarLayer("alice", depID, "evil"), caller: node, code: codes.PermissionDenied},
		{name: "no build", key: appKey, caller: node, code: codes.PermissionDenied,
			edit: func(s *fakeStore) { s.noClaim = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.edit != nil {
				tc.edit(f.store)
			}
			f.objects.blobs[tc.key] = bytes.Repeat([]byte("layer "), 100)
			err := f.client(tc.caller).Sign(context.Background(), tc.key, cosign.SigKeyFor(tc.key))
			if tc.code != codes.OK {
				if status.Code(errors.Unwrap(err)) != tc.code {
					t.Fatalf("Sign = %v, want %s", err, tc.code)
				}
				if _, written := f.objects.blobs[cosign.SigKeyFor(tc.key)]; written {
					t.Fatal("a refused request wrote a signature")
				}
				return
			}
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if err := f.verify(t, tc.key); err != nil {
				t.Fatalf("signd's signature does not verify: %v", err)
			}
		})
	}
}

// A key is signed once. A retry of the same build signs again; a different
// digest for the same key, the swap a compromised node would try, is refused.
func TestSignArtifactOncePerKey(t *testing.T) {
	f := newFixture(t)
	key := storagekey.AppLayer("alice", depID)
	f.objects.blobs[key] = []byte("first build")
	c := f.client(node)
	if err := c.Sign(context.Background(), key, cosign.SigKeyFor(key)); err != nil {
		t.Fatal(err)
	}
	if err := c.Sign(context.Background(), key, cosign.SigKeyFor(key)); err != nil {
		t.Fatalf("retry of the same request: %v", err)
	}
	f.objects.blobs[key] = []byte("swapped content")
	err := c.Sign(context.Background(), key, cosign.SigKeyFor(key))
	if status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("second signature for another digest = %v, want PermissionDenied", err)
	}
	if f.verify(t, key) == nil {
		t.Fatal("the swapped layer verifies")
	}
}

type recordingSigner struct{ keys []string }

func (r *recordingSigner) Sign(_ context.Context, layerKey, _ string) error {
	r.keys = append(r.keys, layerKey)
	return nil
}

func TestClientSendsOnlyAppLayersToSignd(t *testing.T) {
	f := newFixture(t)
	base := storagekey.BaseForArch("node22", "amd64")
	// Without a local key a base is published unsigned: nothing verifies it.
	if err := f.client(node).Sign(context.Background(), base, cosign.SigKeyFor(base)); err != nil {
		t.Fatalf("base layer without a local signer: %v", err)
	}
	if _, written := f.objects.blobs[cosign.SigKeyFor(base)]; written {
		t.Fatal("a base layer was signed with no local signer")
	}
	if len(f.store.rows) != 0 {
		t.Fatal("a base layer reached signd")
	}
	local := &recordingSigner{}
	c := signd.NewClient(direct{srv: f.srv, node: node}, f.objects, local)
	if err := c.Sign(context.Background(), base, cosign.SigKeyFor(base)); err != nil || len(local.keys) != 1 {
		t.Fatalf("base layer: err %v, local signs %v", err, local.keys)
	}
}

func TestDeploymentOfLayerKey(t *testing.T) {
	for key, want := range map[string]string{
		"apps/alice/" + depID + ".ext4":       depID,
		"apps/alice/" + depID + "-proxy.ext4": depID,
		"apps/alice/" + depID + "x.ext4":      "",
		"apps/alice/not-a-uuid.ext4":          "",
		"base/node22-amd64.ext4":              "",
		"apps/a/b/" + depID + ".ext4":         "",
	} {
		got, ok := signd.DeploymentOfLayerKey(key)
		if got != want || ok != (want != "") {
			t.Errorf("DeploymentOfLayerKey(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
}
