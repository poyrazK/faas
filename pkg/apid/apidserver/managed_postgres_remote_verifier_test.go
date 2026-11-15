// adr: 465 — staged credential verification stays with the matching key owner.
package apidserver_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	main "github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/pki"
	"google.golang.org/grpc"
)

type postgresVerificationClient struct {
	secretdpb.SecretdClient
	calls int
	req   *secretdpb.VerifyPostgresCutoverRequest
	err   error
}

func (c *postgresVerificationClient) VerifyPostgresCutover(_ context.Context, req *secretdpb.VerifyPostgresCutoverRequest, _ ...grpc.CallOption) (*secretdpb.VerifyPostgresCutoverResponse, error) {
	c.calls++
	c.req = req
	return &secretdpb.VerifyPostgresCutoverResponse{}, c.err
}

func TestCutoverVerifierDelegatesRuntimeKeysAndKeepsFleetCompatibility(t *testing.T) {
	fleet, _ := age.GenerateX25519Identity()
	runtime, _ := age.GenerateX25519Identity()
	key := []byte(strings.Repeat("k", 32))
	for _, mode := range []string{"runtime", "fleet", "remote_failure", "corrupt_fleet"} {
		t.Run(mode, func(t *testing.T) {
			binding := managedpostgres.Binding{ID: "member", AccountID: "account", AppID: "app", DatabaseID: "target", Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, CredentialGeneration: 1}
			recipient := runtime.Recipient()
			if mode == "fleet" || mode == "corrupt_fleet" {
				recipient = fleet.Recipient()
			}
			material := managedpostgres.CredentialMaterial{ProviderIdentityID: "role", Username: "role", Password: "synthetic-password", Database: "app", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: "db.example.test", Port: 5432}}}
			sealed, err := credentialdelivery.Seal(binding, material, recipient, key)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "corrupt_fleet" {
				sealed.Ciphertext = []byte("broken")
			}
			client := &postgresVerificationClient{}
			if mode == "remote_failure" {
				client.err = errors.New("do not expose transport detail")
			}
			localCalls := 0
			sink := main.NewPostgresCutoverVerifierFixture(fleet, key,
				func(context.Context, string, managedpostgres.CredentialAccess, int) error { localCalls++; return nil }, client)
			c := managedpostgres.Cutover{ID: "cutover", AccountID: "account", AppID: "app", Scope: "default", LeaseToken: "lease", Target: managedpostgres.Database{ID: "target", AccountID: "account", Spec: managedpostgres.Spec{PostgresMajor: 17}}}
			m := managedpostgres.CutoverCredential{ID: "member", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite, Sealed: sealed}
			err = sink.VerifyCutoverCredential(t.Context(), c, m, c.Target)
			if mode == "remote_failure" || mode == "corrupt_fleet" {
				if err == nil {
					t.Fatal("failed verification accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "fleet" || mode == "corrupt_fleet" {
				if client.calls != 0 {
					t.Fatal("fleet failure escalated to remote opener")
				}
				if mode == "fleet" && localCalls != 1 {
					t.Fatal("fleet compatibility lost")
				}
				return
			}
			if client.calls != 1 || localCalls != 0 {
				t.Fatalf("remote=%d local=%d", client.calls, localCalls)
			}
			sum := sha256.Sum256(sealed.Ciphertext)
			if string(client.req.CiphertextSha256) != string(sum[:]) || client.req.LeaseToken != "lease" || client.req.CredentialRef != sealed.Ref {
				t.Fatal("remote verification lost envelope or lease fence")
			}
		})
	}
}

func TestPostgresVerifierConfigurationRejectsPlaintextTransport(t *testing.T) {
	for _, target := range []string{"unix:///tmp/secretd.sock", "http://example.test", "tcp://example.test:8098"} {
		getenv := func(name string) string {
			if name == "FAAS_APID_SECRETD_TARGET" {
				return target
			}
			return ""
		}
		if err := main.CheckPostgresCutoverVerifierConfigurationForTest(t.Context(), getenv); err == nil {
			t.Fatalf("insecure/incomplete verifier config accepted: %s", target)
		}
	}
}

func TestPostgresVerifierConfigurationHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := pki.EnsureCA(root, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range pki.FeatureRoles(pki.FeatureSecretOpener, "control-plane") {
		if role.CommonName != pki.ApidPostgresVerifierClient {
			continue
		}
		if err := pki.EnsureLeaf(root, role, caCert, caKey, false); err != nil {
			t.Fatal(err)
		}
		cert, key := pki.LeafPaths(root, role)
		ca, _ := pki.CARoot(root)
		config := map[string]string{
			"FAAS_APID_SECRETD_TARGET":        "tcp://127.0.0.1:8098",
			"FAAS_APID_SECRETD_TLS_CERT_PATH": cert,
			"FAAS_APID_SECRETD_TLS_KEY_PATH":  key,
			"FAAS_APID_SECRETD_TLS_CA_PATH":   ca,
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := main.CheckPostgresCutoverVerifierConfigurationForTest(ctx, func(name string) string { return config[name] }); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled startup configured a verifier: %v", err)
		}
		return
	}
	t.Fatal("PostgreSQL verifier role is missing")
}
