package state

import (
	"errors"
	"sort"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func testPromotionSecretRetirement(t *testing.T, store Store) {
	f := newPromotionSnapshotFixture(t, store, false)
	f.promotion.ReleaseSecrets = stagePromotionSecretTest(t, f, "api", "TOKEN", f.apps[0].ID)
	f.promotion.HealthPolicy = &api.ProjectPromotionHealthPolicy{}
	for _, app := range f.apps {
		f.promotion.HealthPolicy.Probes = append(f.promotion.HealthPolicy.Probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/health"})
	}
	sort.Slice(f.promotion.HealthPolicy.Probes, func(i, j int) bool {
		return f.promotion.HealthPolicy.Probes[i].AppID < f.promotion.HealthPolicy.Probes[j].AppID
	})
	view, err := store.(ProjectEnvironmentPromotionReleaseConfigurationStore).PreviewProjectEnvironmentPromotionReleaseConfiguration(t.Context(), f.promotion)
	requirePromotionSnapshot(t, err)
	f.view, f.promotion.ReleaseConfigurationHash = view, view.Hash
	f.create(t)
	current, err := age.GenerateX25519Identity()
	requirePromotionSnapshot(t, err)
	maintenance := store.(SecretRekeyStore)
	docs, err := maintenance.ListSecretRekeyDocuments(t.Context(), current.Recipient().String(), "", 100)
	requirePromotionSnapshot(t, err)
	var pending *SecretRekeyDocument
	rotate := func(doc SecretRekeyDocument) {
		next, err := TransformSecretRekeyDocument(doc, func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
			values, err := secretbox.OpenMulti([]*age.X25519Identity{f.identity}, e.Ciphertext)
			if err != nil {
				return e, err
			}
			e.Ciphertext, err = secretbox.Seal(current.Recipient(), values)
			e.Kid = current.Recipient().String()
			return e, err
		})
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, maintenance.CompareAndSwapSecretRekeyDocument(t.Context(), doc, next))
	}
	for _, doc := range docs {
		if doc.Kind == "promotion" {
			copy := doc
			pending = &copy
			continue
		}
		rotate(doc)
	}
	if pending == nil {
		t.Fatal("maintenance omitted private promotion envelopes")
	}
	usage, err := maintenance.SecretEncryptionKeyUsage(t.Context(), f.identity.Recipient().String())
	requirePromotionSnapshot(t, err)
	if usage.ReleaseCaptures != 1 || usage.LiveSecrets != 0 || usage.StagedBundles != 0 || usage.DeploymentSnapshots != 0 {
		t.Fatalf("capture retirement inventory mismatch: %+v", usage)
	}
	if err = maintenance.RetireSecretEncryptionKey(t.Context(), f.identity.Recipient().String()); !errors.Is(err, ErrConflict) {
		t.Fatalf("retired a key needed for promotion recovery: %v", err)
	}
	rotate(*pending)
	requirePromotionSnapshot(t, maintenance.RetireSecretEncryptionKey(t.Context(), f.identity.Recipient().String()))
	view, err = store.(ProjectEnvironmentPromotionReleaseConfigurationStore).ProjectEnvironmentPromotionReleaseConfiguration(t.Context(), f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if view.Hash != f.view.Hash {
		t.Fatal("key retirement changed approved versions")
	}
	f.prepare(t)
	for i, app := range f.apps {
		snapshot, err := store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(t.Context(), f.account.ID, app.ID, f.candidates[i].DeploymentID)
		requirePromotionSnapshot(t, err)
		values, err := secretbox.OpenMulti([]*age.X25519Identity{current}, snapshot.Secrets[0].Ciphertext)
		requirePromotionSnapshot(t, err)
		want := "production-private"
		if i == 0 {
			want = "production-rotated-private"
		}
		if values["TOKEN"] != want {
			t.Fatal("recovery lost destination credentials after old-key retirement")
		}
	}
}

func TestPromotionSecretRetirementMemory(t *testing.T) {
	testPromotionSecretRetirement(t, NewMemStore())
}
