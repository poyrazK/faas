package sealmigrate_test

import (
	"bytes"
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type clusterKeyStore struct {
	key      state.ClusterSigningKey
	missing  bool
	conflict bool
}

func (s *clusterKeyStore) LoadClusterSigningKey(context.Context) (state.ClusterSigningKey, error) {
	if s.missing {
		return state.ClusterSigningKey{}, state.ErrNotFound
	}
	return s.key, nil
}

func (s *clusterKeyStore) ResealClusterSigningKey(_ context.Context, kid string, old, sealed []byte) error {
	if s.conflict || kid != s.key.KeyID || !bytes.Equal(old, s.key.SealedBlob) {
		return state.ErrConflict
	}
	s.key.SealedBlob = sealed
	return nil
}

func TestMigrateControl(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	pemKey := []byte("-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIA==\n-----END PRIVATE KEY-----\n")
	sealed, err := secretbox.SealBytes(fleet.Recipient(), "internal_svc", pemKey, 4096)
	if err != nil {
		t.Fatal(err)
	}
	store := &clusterKeyStore{key: state.ClusterSigningKey{ID: 1, KeyID: "kid", SealedBlob: sealed}}
	report, err := sealmigrate.MigrateControl(context.Background(), store, domain, []*age.X25519Identity{fleet})
	want := sealmigrate.MigrationReport{Domain: sealdomain.Control, Target: domain.Recipient().String(), Scanned: 1, Resealed: 1}
	if err != nil || report != want {
		t.Fatalf("report = %+v, %v; want %+v", report, err, want)
	}
	if ns, plain, err := secretbox.OpenBytes(domain, store.key.SealedBlob); err != nil || ns != "internal_svc" || !bytes.Equal(plain, pemKey) {
		t.Fatalf("domain open = %q, %v", ns, err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, store.key.SealedBlob); err == nil {
		t.Fatal("the fleet identity still opens the re-sealed cluster key")
	}
	again, err := sealmigrate.MigrateControl(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Current != 1 || again.Resealed != 0 {
		t.Fatalf("second run = %+v, %v; want current", again, err)
	}

	rotated := &clusterKeyStore{key: state.ClusterSigningKey{ID: 1, KeyID: "kid", SealedBlob: sealed}, conflict: true}
	if r, err := sealmigrate.MigrateControl(context.Background(), rotated, domain, []*age.X25519Identity{fleet}); err != nil || r.Changed != 1 {
		t.Fatalf("rotated during the run = %+v, %v; want changed", r, err)
	}
	if r, err := sealmigrate.MigrateControl(context.Background(), &clusterKeyStore{missing: true}, domain, []*age.X25519Identity{fleet}); err != nil || r.Scanned != 0 {
		t.Fatalf("no cluster key = %+v, %v; want nothing to do", r, err)
	}
}
