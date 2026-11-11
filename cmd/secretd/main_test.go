package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestConfigFromEnv(t *testing.T) {
	full := map[string]string{
		"FAAS_SECRETD_LISTEN":           "tcp://10.0.0.1:8098",
		"FAAS_SECRETD_TLS_CERT_PATH":    "/etc/faas/tls/secretd/server.crt",
		"FAAS_SECRETD_TLS_KEY_PATH":     "/run/credentials/faas-secretd.service/tls_key",
		"FAAS_SECRETD_TLS_CA_PATH":      "/etc/faas/tls/ca/ca.crt",
		"FAAS_SECRETD_RUNTIME_IDENTITY": "/run/credentials/faas-secretd.service/faas_seal_domain_runtime",
	}
	for _, tc := range []struct {
		name       string
		edit       map[string]string
		want       string
		wantBudget int
	}{
		{name: "complete"},
		{name: "budget", edit: map[string]string{"FAAS_SECRETD_APPS_PER_NODE_HOUR": "250"}, wantBudget: 250},
		{name: "bad budget", edit: map[string]string{"FAAS_SECRETD_APPS_PER_NODE_HOUR": "0"}, want: "positive integer"},
		{name: "unix listener", edit: map[string]string{"FAAS_SECRETD_LISTEN": "unix:///run/faas/secretd.sock"}, want: "want tcp://"},
		{name: "no listener", edit: map[string]string{"FAAS_SECRETD_LISTEN": ""}, want: "want tcp://"},
		{name: "no TLS", edit: map[string]string{"FAAS_SECRETD_TLS_CA_PATH": ""}, want: "mutual TLS only"},
		{name: "no identity", edit: map[string]string{"FAAS_SECRETD_RUNTIME_IDENTITY": ""}, want: "RUNTIME_IDENTITY is required"},
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
				if err != nil || cfg.appsPerNodeHour != tc.wantBudget {
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

func writeIdentity(t *testing.T, path string) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(id.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	return id
}

// The previous identity rides as the systemd credential <name>_previous.
func TestLoadIdentitiesTakesThePreviousCredential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "faas_seal_domain_runtime")
	if _, err := loadIdentities(path); err == nil {
		t.Fatal("no identity loaded without error")
	}
	cur := writeIdentity(t, path)
	prev := writeIdentity(t, path+"_previous")
	ids, err := loadIdentities(path)
	if err != nil || len(ids) != 2 || ids[0].String() != cur.String() || ids[1].String() != prev.String() {
		t.Fatalf("loadIdentities = %d identities, %v", len(ids), err)
	}
}

type registeredNodes map[string]bool

func (r registeredNodes) LookupCN(cn string) error {
	if !r[cn] {
		return errors.New("not a registered compute node")
	}
	return nil
}

type ownedBy struct{ node string }

func (o ownedBy) NodeIDByName(_ context.Context, name string) (string, error) {
	return "id-" + name, nil
}

func (o ownedBy) AppTarget(context.Context, string) (secretd.AppTarget, error) {
	return secretd.AppTarget{OwnerNodeID: "id-" + o.node, AccountID: "acct-1"}, nil
}

func (ownedBy) GrantValid(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (ownedBy) UpsertGrant(_ context.Context, _, _, _ string, at time.Time) (time.Time, error) {
	return at, nil
}

func (ownedBy) RecordOpen(context.Context, string, string, string, int) error { return nil }

// adr: 20261110210000 — the secret-opener feature leaves work end to end:
// vmmd's client leaf, issued for its node, reaches secretd over mutual TLS;
// secretd opens for the app's owner node, refuses another registered node,
// and an unregistered node never completes the handshake.
func TestOpenAppSecretsOverMutualTLS(t *testing.T) {
	root := t.TempDir()
	if _, _, err := pki.EnsureCA(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.EnsureFeatureLeaves(root, root, pki.FeatureSecretOpener, "control-plane"); err != nil {
		t.Fatal(err)
	}
	caPath, _ := pki.CARoot(root)
	serverCert, serverKey := pki.LeafPaths(root, pki.FeatureRoles(pki.FeatureSecretOpener, "control-plane")[0])
	serverTLS, err := wire.LoadServerTLSConfigWithPrefixAndVerifier("secretd_", serverCert, serverKey, caPath,
		registeredNodes{"fsn-2.faas": true, "fsn-3.faas": true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBound([]*age.X25519Recipient{id.Recipient()}, secretbox.Binding{AccountID: "acct-1"},
		secretbox.Envelope{"DATABASE_URL": "postgres://secret"})
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(wire.ServerCredsOrEmpty(serverTLS)...)
	secretdpb.RegisterSecretdServer(srv, secretd.NewServer(ownedBy{node: "fsn-2.faas"}, []*age.X25519Identity{id}, 0,
		slog.New(slog.NewTextHandler(io.Discard, nil))))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	open := func(node string) (map[string]string, error) {
		t.Helper()
		nodeRoot := t.TempDir()
		if _, err := pki.EnsureFeatureLeavesForNode(root, nodeRoot, pki.FeatureSecretOpener, "compute-only", node); err != nil {
			t.Fatal(err)
		}
		clientCert, clientKey := pki.LeafPaths(nodeRoot, pki.FeatureRoles(pki.FeatureSecretOpener, "compute-only")[0])
		clientTLS, err := wire.LoadClientTLSConfigWithPrefix("secretd_", clientCert, clientKey, caPath)
		if err != nil {
			t.Fatal(err)
		}
		clientTLS.ServerName = pki.SecretdServerName // what vmmd dials as
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		resp, err := secretdpb.NewSecretdClient(conn).OpenAppSecrets(context.Background(), &secretdpb.OpenAppSecretsRequest{
			AppId:   "11111111-1111-1111-1111-111111111111",
			Entries: []*secretdpb.SealedEntry{{Key: "DATABASE_URL", Ciphertext: sealed}},
		})
		return resp.GetValues(), err
	}
	if values, err := open("fsn-2.faas"); err != nil || values["DATABASE_URL"] != "postgres://secret" {
		t.Fatalf("the owner node: %v, %v", values, err)
	}
	if _, err := open("fsn-3.faas"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("another registered node = %v, want PermissionDenied", err)
	}
	if _, err := open("fsn-9.faas"); err == nil || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("an unregistered node = %v, want a failed handshake", err)
	}
}
