package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestReleaseSecretRekeyMemory(t *testing.T) {
	testReleaseSecretRekey(t, func(*testing.T) Store { return NewMemStore() })
}

func TestSecretRekeyFencesManagedOwnershipMemory(t *testing.T) {
	store := NewMemStore()
	id, err := age.GenerateX25519Identity()
	mustReleaseSecret(t, err)
	cipher, err := secretbox.SealOne(id.Recipient(), "TOKEN", "managed-private", 4096)
	mustReleaseSecret(t, err)
	key := secretKey{AppID: "app", Scope: "production", Key: "TOKEN"}
	store.secrets[key] = AppSecret{AccountID: "account", AppID: "app", Scope: "production", Key: "TOKEN", Kid: id.Recipient().String(), Ciphertext: cipher, ManagedCredentialRef: "original", ManagedCredentialGeneration: 1}
	next, err := age.GenerateX25519Identity()
	mustReleaseSecret(t, err)
	docs, err := store.ListSecretRekeyDocuments(t.Context(), next.Recipient().String(), "", 1)
	mustReleaseSecret(t, err)
	replacement, err := TransformSecretRekeyDocument(docs[0], func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
		e.Kid = next.Recipient().String()
		e.Ciphertext, err = secretbox.SealOne(next.Recipient(), e.Key, "managed-private", 4096)
		return e, err
	})
	mustReleaseSecret(t, err)
	row := store.secrets[key]
	row.ManagedCredentialRef = "replacement"
	row.ManagedCredentialGeneration = 2
	store.secrets[key] = row
	if err := store.CompareAndSwapSecretRekeyDocument(t.Context(), docs[0], replacement); !errors.Is(err, ErrConflict) {
		t.Fatal("stale envelope maintenance crossed a managed ownership change")
	}
}
func testReleaseSecretRekey(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"before_candidate", "after_candidate", "concurrent_replacement", "logical_mutation", "unlabelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			store := factory(t)
			account, err := store.CreateAccount(ctx, "snapshot-rekey@example.test", api.PlanPro)
			mustReleaseSecret(t, err)
			project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "rekey"})
			mustReleaseSecret(t, err)
			app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "rekey-api", WorkloadName: "api", WorkloadClass: WorkloadClassHTTP, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
			mustReleaseSecret(t, err)
			previous, err := age.GenerateX25519Identity()
			mustReleaseSecret(t, err)
			current, err := age.GenerateX25519Identity()
			mustReleaseSecret(t, err)
			oldKid, newKid := previous.Recipient().String(), current.Recipient().String()
			cipher, err := secretbox.SealOne(previous.Recipient(), "TOKEN", "baseline-private", 4096)
			mustReleaseSecret(t, err)
			kid := oldKid
			if scenario == "unlabelled" {
				kid = ""
			}
			mustReleaseSecret(t, store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "production", "TOKEN", kid, "0123456789abcdef", api.SecretClassPersistent, cipher))
			old, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball})
			mustReleaseSecret(t, err)
			mustReleaseSecret(t, store.MarkDeploymentLive(ctx, old.ID))
			base, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{{app.ID, old.ID}})
			mustReleaseSecret(t, err)
			cipher, err = secretbox.SealOne(previous.Recipient(), "TOKEN", "candidate-private", 4096)
			mustReleaseSecret(t, err)
			staged, err := store.(ProjectReleaseSecretsStore).StageProjectReleaseSecrets(ctx, account.ID, project.ID, strings.Repeat("a", 32), strings.Repeat("b", 64), []ProjectReleaseSealedSecret{{Workload: "api", AppID: app.ID, Key: "TOKEN", Kid: oldKid, ValueHash: "fedcba9876543210", Ciphertext: cipher}}, map[string]map[string]string{"api": {"TOKEN": "candidate-private"}})
			mustReleaseSecret(t, err)
			request, _ := json.Marshal(api.ProjectReleaseConfigRequest{Secrets: staged.Versions})
			configStore := store.(ProjectReleaseConfigurationStore)
			config, err := configStore.PreviewProjectReleaseConfiguration(ctx, account.ID, project.ID, base.ID, request)
			mustReleaseSecret(t, err)
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: base.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Configuration: config, Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/health"}}}}
			attempt := strings.Repeat("c", 32)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", SelectionComplete: true, Status: "complete", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			_, err = store.(ProjectSubmissionStore).ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("d", 64), receipt)
			mustReleaseSecret(t, err)
			mustReleaseSecret(t, configStore.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, request))
			proofBefore, _ := json.Marshal(safe)
			candidateCtx := ContextWithProjectReleaseConfigurationAttempt(ContextWithDeploymentSubmission(ctx, ProjectWorkloadAttempt(attempt, app.ID)), attempt)
			create := func() Deployment {
				d, err := store.CreateDeployment(candidateCtx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true})
				mustReleaseSecret(t, err)
				return d
			}
			var candidate Deployment
			if scenario == "after_candidate" {
				candidate = create()
			}
			runtimeStore := store.(RuntimeAppValuesStore)
			before, err := runtimeStore.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, old.ID)
			mustReleaseSecret(t, err)
			oldFence, err := NewRuntimeAppSecretFence(before)
			mustReleaseSecret(t, err)
			maintenance := store.(SecretRekeyStore)
			usage, err := maintenance.SecretEncryptionKeyUsage(ctx, oldKid)
			mustReleaseSecret(t, err)
			if usage.LiveSecrets != 1 || usage.StagedBundles != 1 || usage.ReleaseCaptures != 1 || usage.DeploymentSnapshots < 1 {
				t.Fatalf("incomplete key inventory: %+v", usage)
			}
			if err := maintenance.RetireSecretEncryptionKey(ctx, oldKid); !errors.Is(err, ErrConflict) {
				t.Fatal("retired a referenced key")
			}
			transform := func(doc SecretRekeyDocument) json.RawMessage {
				out, err := TransformSecretRekeyDocument(doc, func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
					values, err := secretbox.OpenMulti([]*age.X25519Identity{current, previous}, e.Ciphertext)
					if err != nil {
						return e, err
					}
					e.Ciphertext, err = secretbox.Seal(current.Recipient(), values)
					e.Kid = newKid
					return e, err
				})
				mustReleaseSecret(t, err)
				return out
			}
			docs, err := maintenance.ListSecretRekeyDocuments(ctx, newKid, "", 100)
			mustReleaseSecret(t, err)
			if scenario == "logical_mutation" {
				var found bool
				for _, doc := range docs {
					if doc.Kind == "deployment" {
						found = true
						out := transform(doc)
						out = bytes.Replace(out, []byte(before.ReleaseSecretVersionIDs["TOKEN"]), []byte("00000000-0000-4000-8000-000000000001"), 1)
						if err := maintenance.CompareAndSwapSecretRekeyDocument(ctx, doc, out); !errors.Is(err, ErrInvalidArgument) {
							t.Fatal("maintenance changed an immutable version")
						}
					}
				}
				if !found {
					t.Fatal("missing retained deployment")
				}
			}
			if scenario == "concurrent_replacement" {
				doc := docs[0]
				out := transform(doc)
				mustReleaseSecret(t, maintenance.CompareAndSwapSecretRekeyDocument(ctx, doc, out))
				if err := maintenance.CompareAndSwapSecretRekeyDocument(ctx, doc, transform(doc)); !errors.Is(err, ErrConflict) {
					t.Fatal("stale rekey overwrote a concurrent replacement")
				}
			}
			// A fresh scan models a restart; already migrated documents disappear.
			docs, err = maintenance.ListSecretRekeyDocuments(ctx, newKid, "", 100)
			mustReleaseSecret(t, err)
			for _, doc := range docs {
				mustReleaseSecret(t, maintenance.CompareAndSwapSecretRekeyDocument(ctx, doc, transform(doc)))
			}
			remaining, err := maintenance.ListSecretRekeyDocuments(ctx, newKid, "", 100)
			mustReleaseSecret(t, err)
			if len(remaining) != 0 {
				t.Fatal("rekey left outstanding encrypted copies")
			}
			usage, err = maintenance.SecretEncryptionKeyUsage(ctx, oldKid)
			mustReleaseSecret(t, err)
			if !usage.Empty() {
				t.Fatal("old key still required after rekey")
			}
			mustReleaseSecret(t, maintenance.RetireSecretEncryptionKey(ctx, oldKid))
			mustReleaseSecret(t, maintenance.RetireSecretEncryptionKey(ctx, oldKid))
			if err := store.UpsertAppSecretWithKidInScope(ctx, account.ID, app.ID, "staging", "STALE", oldKid, cipher); !errors.Is(err, ErrConflict) {
				t.Fatal("writer published with a retired key")
			}
			if err := store.UpsertAppSecretInScope(ctx, account.ID, app.ID, "staging", "UNLABELLED", cipher); !errors.Is(err, ErrConflict) {
				t.Fatal("writer bypassed retirement without a key ID")
			}
			mustReleaseSecret(t, configStore.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, request))
			if candidate.ID == "" {
				candidate = create()
			}
			for _, check := range []struct {
				deployment string
				want       string
			}{{old.ID, "baseline-private"}, {candidate.ID, "candidate-private"}} {
				snapshot, err := runtimeStore.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, check.deployment)
				mustReleaseSecret(t, err)
				values, err := secretbox.Open(current, snapshot.Secrets[0].Ciphertext)
				mustReleaseSecret(t, err)
				if values["TOKEN"] != check.want || snapshot.Secrets[0].Kid != newKid {
					t.Fatal("new identity cannot deliver the retained release")
				}
				if _, err := secretbox.Open(previous, snapshot.Secrets[0].Ciphertext); err == nil {
					t.Fatal("envelope still opens with the retired identity")
				}
			}
			after, err := runtimeStore.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, old.ID)
			mustReleaseSecret(t, err)
			fence, err := NewRuntimeAppSecretFence(after)
			mustReleaseSecret(t, err)
			if after.ReleaseSecretVersionIDs["TOKEN"] != before.ReleaseSecretVersionIDs["TOKEN"] || after.Secrets[0].DeliveryVersion != before.Secrets[0].DeliveryVersion || fence.Fingerprint == oldFence.Fingerprint {
				t.Fatal("logical version or envelope publication fence was lost")
			}
			proofAfter, _ := json.Marshal(safe)
			if !bytes.Equal(proofBefore, proofAfter) {
				t.Fatal("rekey changed the public release proof")
			}
			// The saved proof must still detect a real configuration change.
			mustReleaseSecret(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "CHANGED", "operator"))
			if err := configStore.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, request); !errors.Is(err, ErrConflict) {
				t.Fatal("rekey compatibility bypassed configuration mutation")
			}
		})
	}
}
