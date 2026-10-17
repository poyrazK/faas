package state

import (
	"encoding/json"
	"errors"
	"maps"
	"sort"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestPromotionSecretsMemory(t *testing.T) {
	testPromotionSecrets(t, func(*testing.T) Store { return NewMemStore() })
}

func stagePromotionSecretTest(t *testing.T, f *promotionSnapshotFixture, name, key, app string) map[string]map[string]string {
	t.Helper()
	cipher, err := secretbox.SealOne(f.identity.Recipient(), key, "production-rotated-private", 4096)
	requirePromotionSnapshot(t, err)
	response, err := f.store.(ProjectReleaseSecretsStore).StageProjectReleaseSecrets(t.Context(), f.account.ID, f.project.ID, strings.Repeat("a", 32), strings.Repeat("f", 64), []ProjectReleaseSealedSecret{{Workload: name, AppID: app, Key: key, Kid: f.identity.Recipient().String(), ValueHash: strings.Repeat("e", 64), Ciphertext: cipher}}, map[string]map[string]string{name: {key: "production-rotated-private"}})
	requirePromotionSnapshot(t, err)
	return response.Versions
}

func testPromotionSecrets(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"rotate", "remove", "pinned", "expired_staging", "rekey_before_admission", "rekey_before_candidate", "rekey_after_candidate", "foreign_workload", "new_key", "stale_preview", "candidate_tamper", "rollback_operator_edit", "public_copy"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPromotionSnapshotFixture(t, factory(t), false)
			ctx := t.Context()
			if scenario == "pinned" {
				pinPromotionSecretSnapshots(t, f)
			}
			key, app := "TOKEN", f.apps[0].ID
			if scenario == "new_key" {
				key = "NEW_TOKEN"
			}
			if scenario == "foreign_workload" {
				app = f.apps[1].ID
			}
			f.promotion.ReleaseSecrets = stagePromotionSecretTest(t, f, "api", key, app)
			if scenario == "remove" {
				f.promotion.ReleaseSecrets = map[string]map[string]string{"api": {}}
			}
			policy := &api.ProjectPromotionHealthPolicy{}
			for _, app := range f.apps {
				policy.Probes = append(policy.Probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/health"})
			}
			sort.Slice(policy.Probes, func(i, j int) bool { return policy.Probes[i].AppID < policy.Probes[j].AppID })
			f.promotion.HealthPolicy = policy
			view, err := f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).PreviewProjectEnvironmentPromotionReleaseConfiguration(ctx, f.promotion)
			if scenario == "foreign_workload" || scenario == "new_key" {
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("invalid destination version admitted: %v", err)
				}
				return
			}
			requirePromotionSnapshot(t, err)
			f.view, f.promotion.ReleaseConfigurationHash = view, view.Hash
			if scenario == "stale_preview" {
				changePromotionSecret(t, f)
				if _, _, err = f.store.CreateProjectEnvironmentPromotion(ctx, f.promotion, f.workloads); !errors.Is(err, ErrConflict) {
					t.Fatalf("stale secret preview admitted: %v", err)
				}
				previous, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, f.apps[0].ID, f.previous[0].ID)
				requirePromotionSnapshot(t, err)
				if previous.ReleaseSecretsPinned {
					t.Fatal("failed creation froze secret intent")
				}
				return
			}
			if scenario == "rekey_before_admission" {
				rekeyUnadmittedPromotionSecrets(t, f)
			}
			f.create(t)
			if scenario == "public_copy" {
				view, err = f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).ProjectEnvironmentPromotionReleaseConfiguration(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				view.Secrets.Versions["api"]["TOKEN"] = "changed"
				view, err = f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).ProjectEnvironmentPromotionReleaseConfiguration(ctx, f.account.ID, f.promotion.ID)
				requirePromotionSnapshot(t, err)
				if view.Secrets.Versions["api"]["TOKEN"] == "changed" {
					t.Fatal("public receipt mutated private capture")
				}
			}
			if scenario == "expired_staging" {
				discardPromotionSecretStaging(t, f)
			}
			if scenario == "rekey_before_candidate" {
				rekeyPromotionSecrets(t, f)
			}
			f.prepare(t)
			if scenario == "rekey_after_candidate" {
				rekeyPromotionSecrets(t, f)
			}
			for i, app := range f.apps {
				current, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, f.candidates[i].DeploymentID)
				requirePromotionSnapshot(t, err)
				if !current.ReleaseSecretsPinned || !maps.Equal(current.ReleaseSecretVersionIDs, f.view.Secrets.Versions[app.WorkloadName]) {
					t.Fatal("candidate lost approved secret versions")
				}
				previous, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, f.previous[i].ID)
				requirePromotionSnapshot(t, err)
				if !previous.ReleaseSecretsPinned || !maps.Equal(previous.ReleaseSecretVersionIDs, f.view.Secrets.PreviousVersions[app.WorkloadName]) {
					t.Fatal("rollback baseline not frozen")
				}
				if scenario != "remove" && !strings.HasPrefix(scenario, "rekey") {
					values, err := secretbox.OpenMulti([]*age.X25519Identity{f.identity}, current.Secrets[0].Ciphertext)
					requirePromotionSnapshot(t, err)
					want := "production-private"
					if i == 0 {
						want = "production-rotated-private"
					}
					if values["TOKEN"] != want {
						t.Fatal("candidate delivered wrong environment credentials")
					}
				}
			}
			healthStore := f.store.(ProjectPromotionHealthStore)
			h, err := healthStore.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			h.Phase, h.Verdict, h.HealthyRounds = "cutover", "healthy", api.ProjectReleaseVerificationRounds
			checked := time.Now().UTC()
			h.CheckedAt, h.Evidence = &checked, promotionHealthEvidence(h.Policy, 0, 0)
			setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
			if scenario == "candidate_tamper" {
				tamperPromotionSecretCandidate(t, f)
			}
			publisher := f.store.(ProjectEnvironmentPromotionReleaseSetStore)
			_, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
			if scenario == "candidate_tamper" {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("wrong candidate secret published: %v", err)
				}
				return
			}
			requirePromotionSnapshot(t, err)
			h, err = healthStore.ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			h.Phase = "rolling_back"
			checked = time.Now().UTC()
			h.CheckedAt, h.Evidence = &checked, promotionHealthEvidence(h.Policy, 0, 0)
			setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
			if scenario == "rollback_operator_edit" {
				requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, "production", "MODE", "operator-edit"))
			}
			_, err = publisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.targetGraph.Members)
			if scenario == "rollback_operator_edit" {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("rollback overwrote operator edit: %v", err)
				}
				return
			}
			requirePromotionSnapshot(t, err)
			active, err := f.store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
			requirePromotionSnapshot(t, err)
			for _, member := range active.Members {
				if releaseMemberForApp(f.targetGraph, member.AppID) != member.DeploymentID {
					t.Fatal("rollback lost original code and credentials")
				}
			}
		})
	}
}

func discardPromotionSecretStaging(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	switch store := f.store.(type) {
	case *MemStore:
		store.mu.Lock()
		store.releaseSecretBundles = map[string]releaseSecretBundle{}
		store.mu.Unlock()
	case *PgStore:
		_, err := store.pool.Exec(t.Context(), "DELETE FROM project_release_secret_bundles WHERE project_id=$1", f.project.ID)
		requirePromotionSnapshot(t, err)
	}
}

func tamperPromotionSecretCandidate(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	switch store := f.store.(type) {
	case *MemStore:
		store.mu.Lock()
		store.releaseSecretPins[f.candidates[0].DeploymentID] = []releaseSecretVersion{}
		store.mu.Unlock()
	case *PgStore:
		// Deliberately corrupt a fixture through the owner teardown path.
		_, err := store.pool.Exec(t.Context(), "ALTER TABLE deployment_release_secret_snapshots DISABLE TRIGGER USER")
		requirePromotionSnapshot(t, err)
		defer func() {
			_, err := store.pool.Exec(t.Context(), "ALTER TABLE deployment_release_secret_snapshots ENABLE TRIGGER USER")
			requirePromotionSnapshot(t, err)
		}()
		_, err = store.pool.Exec(t.Context(), "UPDATE deployment_release_secret_snapshots SET values_json='[]' WHERE deployment_id=$1", f.candidates[0].DeploymentID)
		requirePromotionSnapshot(t, err)
	}
}

func rekeyUnadmittedPromotionSecrets(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	current, err := age.GenerateX25519Identity()
	requirePromotionSnapshot(t, err)
	store := f.store.(SecretRekeyStore)
	docs, err := store.ListSecretRekeyDocuments(t.Context(), current.Recipient().String(), "", 100)
	requirePromotionSnapshot(t, err)
	for _, doc := range docs {
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
		requirePromotionSnapshot(t, store.CompareAndSwapSecretRekeyDocument(t.Context(), doc, next))
	}
	view, err := f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).PreviewProjectEnvironmentPromotionReleaseConfiguration(t.Context(), f.promotion)
	if err != nil || view.Hash != f.view.Hash {
		t.Fatal("rekey invalidated reviewed versions", err)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "ciphertext") {
		t.Fatal("public preview exposed credentials")
	}
}
