package secretd_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type postgresVerificationStore struct {
	*fakeStore
	credential secretd.PostgresCredential
	current    bool
	audits     int
	auditError bool
}

func TestPostgresVerifierCannotOpenSecretsAsRegisteredNode(t *testing.T) {
	for _, registered := range []bool{false, true} {
		t.Run(map[bool]string{false: "unregistered", true: "registered"}[registered], func(t *testing.T) {
			f := newFixture(t, 0)
			f.caller = pki.ApidPostgresVerifierClient
			if registered {
				f.store.nodes[f.caller] = "n-owner"
			}
			entry := &secretdpb.SealedEntry{Key: "KEY", Ciphertext: f.seal(t, "acct-a", secretbox.Envelope{"KEY": "synthetic-secret"})}
			_, err := f.server.OpenAppSecrets(t.Context(), &secretdpb.OpenAppSecretsRequest{AppId: "app-a", Entries: []*secretdpb.SealedEntry{entry}})
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("verify-only identity gained ordinary secret access: %v", err)
			}
			if len(f.store.opens) != 0 {
				t.Fatal("verify-only identity reached the ordinary secret audit")
			}
		})
	}
}

func (s *postgresVerificationStore) PostgresCutoverCredential(_ context.Context, a, c, m, lease string) (secretd.PostgresCredential, error) {
	if !s.current || a != "account-1" || c != "cutover-1" || m != "member-1" || lease != "lease-1" {
		return secretd.PostgresCredential{}, secretd.ErrUnknownPostgresCredential
	}
	return s.credential, nil
}
func (s *postgresVerificationStore) RecordPostgresVerification(context.Context, string, string, string, string) error {
	if s.auditError {
		return errors.New("audit unavailable")
	}
	s.audits++
	return nil
}

func TestPostgresCutoverVerificationKeepsRuntimeCustody(t *testing.T) {
	for _, mode := range []string{"valid", "compute_caller", "unknown_caller", "stale_lease", "changed_target", "other_account", "other_member", "digest", "kid", "ref", "stored_ref", "hash", "fingerprint", "other_bound_account", "corrupt", "audit_failure", "sql_failure", "rotation", "legacy_unbound"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, 0)
			runtime, _ := age.GenerateX25519Identity()
			next, _ := age.GenerateX25519Identity()
			key := []byte(strings.Repeat("k", 32))
			value := "postgresql://pg-user:synthetic-password@db.example.test/app?sslmode=require"
			bound := secretbox.Binding{AccountID: "account-1"}
			if mode == "other_bound_account" {
				bound.AccountID = "another-account"
			}
			if mode == "legacy_unbound" {
				bound = secretbox.Binding{}
			}
			cipher, err := secretbox.SealOneBound(runtime.Recipient(), bound, "DATABASE_URL", value, 32<<10)
			if err != nil {
				t.Fatal(err)
			}
			hash, _ := secretbox.ValueFingerprint([]byte(value), key)
			ref, err := credentialdelivery.Ref(managedpostgres.Binding{ID: "member-1", CredentialGeneration: 1})
			if err != nil {
				t.Fatal(err)
			}
			store := &postgresVerificationStore{fakeStore: f.store, current: true, credential: secretd.PostgresCredential{AccountID: "account-1", AppID: "app-1", Key: "DATABASE_URL", Access: "read_write", ProviderIdentity: "pg-user", Ref: ref, Kid: runtime.Recipient().String(), ValueHash: hash, Ciphertext: cipher, Major: 17}}
			caller := pki.ApidPostgresVerifierClient
			calls := 0
			server := secretd.NewServer(store, []*age.X25519Identity{next, runtime}, 100, nil).WithPeerNode(func(context.Context) (string, error) { return caller, nil })
			reg := prometheus.NewRegistry()
			metrics, err := secretd.NewMetrics(reg)
			if err != nil {
				t.Fatal(err)
			}
			server.WithMetrics(metrics)
			server.WithPostgresVerification(key, func(_ context.Context, dsn string, access managedpostgres.CredentialAccess, major int) error {
				calls++
				if store.audits == 0 {
					t.Fatal("SQL before audit")
				}
				if dsn != value || access != managedpostgres.CredentialReadWrite || major != 17 {
					t.Fatal("wrong credential probe")
				}
				if mode == "sql_failure" {
					return errors.New("synthetic-password must never appear in RPC errors")
				}
				return nil
			})
			sum := sha256.Sum256(cipher)
			req := &secretdpb.VerifyPostgresCutoverRequest{AccountId: "account-1", CutoverId: "cutover-1", CredentialId: "member-1", LeaseToken: "lease-1", CiphertextSha256: sum[:], Kid: store.credential.Kid, ValueHash: hash, CredentialRef: ref}
			want := codes.FailedPrecondition
			switch mode {
			case "valid", "rotation", "legacy_unbound":
				want = codes.OK
			case "compute_caller":
				caller = "owner-node"
				want = codes.PermissionDenied
			case "unknown_caller":
				caller = ""
				want = codes.PermissionDenied
			case "stale_lease":
				req.LeaseToken = "old"
			case "changed_target":
				store.current = false
			case "other_account":
				req.AccountId = "another-account"
			case "other_member":
				req.CredentialId = "other-member"
			case "digest":
				req.CiphertextSha256 = make([]byte, 32)
			case "kid":
				req.Kid = "other"
			case "ref":
				req.CredentialRef = "other"
			case "stored_ref":
				store.credential.Ref = "other"
				req.CredentialRef = "other"
			case "hash":
				req.ValueHash = "other"
			case "fingerprint":
				store.credential.ValueHash = "bad"
				req.ValueHash = "bad"
			case "corrupt":
				store.credential.Ciphertext = []byte("broken")
				sum = sha256.Sum256(store.credential.Ciphertext)
				req.CiphertextSha256 = sum[:]
			case "audit_failure":
				store.auditError = true
				want = codes.Unavailable
			}
			resp, err := server.VerifyPostgresCutover(t.Context(), req)
			if status.Code(err) != want {
				t.Fatalf("got %v, want %v", err, want)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-password") {
				t.Fatal("SQL credential leaked in error")
			}
			if want == codes.OK && (resp == nil || proto.Size(resp) != 0 || calls != 1) {
				t.Fatalf("response=%v calls=%d", resp, calls)
			}
			if want != codes.OK && mode != "sql_failure" && calls != 0 {
				t.Fatalf("refused request reached SQL: %d", calls)
			}
			metric, label, labelValue := "faas_secretd_opens_total", "via", "verification"
			if want != codes.OK {
				metric, label, labelValue = "faas_secretd_refusals_total", "reason", secretd.ReasonEnvelope
				if want == codes.PermissionDenied {
					labelValue = secretd.ReasonNotAuthorized
				}
			}
			if got := counter(t, reg, metric, map[string]string{"rpc": "VerifyPostgresCutover", label: labelValue}); got != 1 {
				t.Fatalf("verification metric=%v, want 1", got)
			}
			// The control-plane verification leaf gains no runtime-secret opens.
			if mode == "valid" {
				_, err := server.OpenAppSecrets(t.Context(), &secretdpb.OpenAppSecretsRequest{AppId: "app-a"})
				if status.Code(err) != codes.PermissionDenied {
					t.Fatalf("verifier gained ordinary secret opens: %v", err)
				}
			}
		})
	}
}
