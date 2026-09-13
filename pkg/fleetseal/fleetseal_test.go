package fleetseal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeStore struct {
	cluster state.ClusterSigningKey
	secrets []state.AppSecret
	probe   state.FleetSealProbe
}

func (s *fakeStore) LoadClusterSigningKey(context.Context) (state.ClusterSigningKey, error) {
	return s.cluster, nil
}
func (s *fakeStore) ResealClusterSigningKey(_ context.Context, _ string, old, sealed []byte) error {
	s.cluster.SealedBlob = append([]byte(nil), sealed...)
	return nil
}
func (s *fakeStore) ListAppSecretsForRekey(_ context.Context, _ int, cursor string) ([]state.AppSecret, error) {
	if cursor != "" {
		return nil, nil
	}
	return append([]state.AppSecret(nil), s.secrets...), nil
}
func (s *fakeStore) ResealAppSecretForFleet(_ context.Context, row state.AppSecret, recipient string, sealed []byte) error {
	for i := range s.secrets {
		if s.secrets[i].AppID == row.AppID && s.secrets[i].Scope == row.Scope && s.secrets[i].Key == row.Key {
			s.secrets[i].Kid = recipient
			s.secrets[i].Ciphertext = append([]byte(nil), sealed...)
		}
	}
	return nil
}
func (s *fakeStore) LoadFleetSealProbe(context.Context) (state.FleetSealProbe, error) {
	return s.probe, nil
}
func (s *fakeStore) UpsertFleetSealProbe(_ context.Context, recipient string, sealed []byte) error {
	s.probe = state.FleetSealProbe{Recipient: recipient, SealedBlob: append([]byte(nil), sealed...), UpdatedAt: time.Now()}
	return nil
}

func TestMigrateAndVerifyAcrossTwelveUniqueHosts(t *testing.T) {
	legacy, _ := age.GenerateX25519Identity()
	fleet, _ := age.GenerateX25519Identity()
	privatePEM, publicPEM, kid := signingKey(t)
	clusterBlob, err := secretbox.SealBytes(legacy.Recipient(), "internal_svc", privatePEM, 4096)
	if err != nil {
		t.Fatal(err)
	}
	appBlob, err := secretbox.SealOne(legacy.Recipient(), "DATABASE_URL", "postgres://fixture", 1024)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		cluster: state.ClusterSigningKey{ID: 1, KeyID: kid, PublicKeyPEM: string(publicPEM), SealedBlob: clusterBlob},
		secrets: []state.AppSecret{{AccountID: "a", AppID: "b", Scope: state.DefaultEnvScope, Key: "DATABASE_URL", Kid: legacy.Recipient().String(), Ciphertext: appBlob}},
	}
	report, err := Migrate(context.Background(), store, fleet, []*age.X25519Identity{legacy})
	if err != nil {
		t.Fatal(err)
	}
	if report.SecretsResealed != 1 || !report.ProbeWritten || report.ClusterKID != kid {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, _, err := secretbox.OpenBytes(legacy, store.cluster.SealedBlob); err == nil {
		t.Fatal("cluster key still opens with legacy-only identity")
	}
	if _, err := secretbox.Open(legacy, store.secrets[0].Ciphertext); err == nil {
		t.Fatal("customer secret still opens with legacy-only identity")
	}

	seenHosts := map[string]struct{}{}
	for i := 0; i < 12; i++ {
		host, _ := age.GenerateX25519Identity()
		seenHosts[host.Recipient().String()] = struct{}{}
		verified, err := Verify(context.Background(), store, fleet, host)
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
		if !verified.Ready || !verified.ProbeOK || !verified.JWTRoundTripOK || verified.ClusterKID != kid {
			t.Fatalf("node %d: %+v", i, verified)
		}
	}
	if len(seenHosts) != 12 {
		t.Fatalf("unique per-host identities=%d, want 12", len(seenHosts))
	}
}

func TestVerifyRejectsHostIdentityEqualToFleet(t *testing.T) {
	fleet, _ := age.GenerateX25519Identity()
	if _, err := Verify(context.Background(), &fakeStore{}, fleet, fleet); err == nil {
		t.Fatal("expected replaced host identity to fail")
	}
}

func signingKey(t *testing.T) ([]byte, []byte, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, _ := x509.MarshalPKCS8PrivateKey(priv)
	publicDER, _ := x509.MarshalPKIXPublicKey(pub)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}),
		internalsvc.KidFromPub(pub)
}
