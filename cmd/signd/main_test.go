package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	signdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/signd/v1"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestConfigFromEnv(t *testing.T) {
	full := map[string]string{
		"FAAS_SIGND_LISTEN":        "tcp://10.0.0.1:7443",
		"FAAS_SIGND_TLS_CERT_PATH": "/etc/faas/tls/signd/server.crt",
		"FAAS_SIGND_TLS_KEY_PATH":  "/etc/faas/tls/signd/server.key",
		"FAAS_SIGND_TLS_CA_PATH":   "/etc/faas/tls/ca.crt",
	}
	for _, tc := range []struct {
		name string
		edit map[string]string
		want string
	}{
		{name: "complete"},
		{name: "unix listener", edit: map[string]string{"FAAS_SIGND_LISTEN": "unix:///run/faas/signd.sock"}, want: "want tcp://"},
		{name: "no listener", edit: map[string]string{"FAAS_SIGND_LISTEN": ""}, want: "want tcp://"},
		{name: "no TLS", edit: map[string]string{"FAAS_SIGND_TLS_CA_PATH": ""}, want: "mutual TLS only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range full {
				env[k] = v
			}
			for k, v := range tc.edit {
				env[k] = v
			}
			cfg, err := configFromEnv(func(k string) string { return env[k] })
			if tc.want == "" {
				if err != nil || cfg.signKeyPath != cosign.DefaultSignKeyPath {
					t.Fatalf("configFromEnv = %+v, %v", cfg, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("configFromEnv error = %v, want %q", err, tc.want)
			}
		})
	}
}

type registeredNodes map[string]bool

func (r registeredNodes) LookupCN(cn string) error {
	if !r[cn] {
		return errors.New("not a registered compute node")
	}
	return nil
}

type buildingOn struct{ node string }

func (b buildingOn) BuildClaim(context.Context, string) (signd.Claim, error) {
	return signd.Claim{Status: state.DeployImaging, AppSlug: "alice", NodeName: b.node, Phase: state.ImagePreparing}, nil
}

func (buildingOn) RecordSignature(_ context.Context, r signd.Record) (signd.Record, bool, error) {
	return r, true, nil
}

// adr: 20261109170000 — the artifact-signer feature leaves work end to end:
// imaged's client leaf, issued for its node, reaches signd over mutual TLS;
// signd signs for the node building the deployment, refuses a registered
// node that is not, and an unregistered node never completes the handshake.
func TestSignArtifactOverMutualTLS(t *testing.T) {
	root := t.TempDir()
	if _, _, err := pki.EnsureCA(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.EnsureFeatureLeaves(root, root, pki.FeatureArtifactSigner, "control-plane"); err != nil {
		t.Fatal(err)
	}
	caPath, _ := pki.CARoot(root)
	serverCert, serverKey := pki.LeafPaths(root, pki.FeatureRoles(pki.FeatureArtifactSigner, "control-plane")[0])
	serverTLS, err := wire.LoadServerTLSConfigWithPrefixAndVerifier("signd_", serverCert, serverKey, caPath,
		registeredNodes{"fsn-2.faas": true, "fsn-3.faas": true})
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(wire.ServerCredsOrEmpty(serverTLS)...)
	signdpb.RegisterSigndServer(srv, signd.NewServer(buildingOn{node: "fsn-2.faas"}, key, objects, nil))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	const deployment = "11111111-1111-1111-1111-111111111111"
	layer := storagekey.AppLayer("alice", deployment)
	sign := func(node string) error {
		t.Helper()
		nodeRoot := t.TempDir()
		if _, err := pki.EnsureFeatureLeavesForNode(root, nodeRoot, pki.FeatureArtifactSigner, "compute-only", node); err != nil {
			t.Fatal(err)
		}
		clientCert, clientKey := pki.LeafPaths(nodeRoot, pki.FeatureRoles(pki.FeatureArtifactSigner, "compute-only")[0])
		clientTLS, err := wire.LoadClientTLSConfigWithPrefix("signd_", clientCert, clientKey, caPath)
		if err != nil {
			t.Fatal(err)
		}
		clientTLS.ServerName = pki.SigndServerName // what imaged dials as
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_, err = signdpb.NewSigndClient(conn).SignArtifact(context.Background(), &signdpb.SignArtifactRequest{
			DeploymentId: deployment, StorageKey: layer, Digest: strings.Repeat("ab", 32), Bytes: 4096})
		return err
	}
	if err := sign("fsn-2.faas"); err != nil {
		t.Fatalf("the building node: %v", err)
	}
	if rc, err := objects.Get(context.Background(), cosign.SigKeyFor(layer)); err != nil {
		t.Fatalf("signd wrote no signature: %v", err)
	} else {
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		_ = rc.Close()
		if !strings.Contains(b.String(), `"key":"`+layer+`"`) {
			t.Fatalf("signature does not name the layer: %s", b.String())
		}
	}
	if err := sign("fsn-3.faas"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("another registered node = %v, want PermissionDenied", err)
	}
	if err := sign("fsn-9.faas"); err == nil || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("an unregistered node = %v, want a failed handshake", err)
	}
}
