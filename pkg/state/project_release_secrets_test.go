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

func TestProjectReleaseSecretsMemory(t *testing.T) {
	testProjectReleaseSecrets(t, func(*testing.T) Store { return NewMemStore() })
}
func testProjectReleaseSecrets(t *testing.T, factory func(*testing.T) Store) {
	for _, outcome := range []string{"success", "rollback", "rekey_rollback", "inherit", "empty", "candidate_failed", "wrong_key", "wrong_owner", "expired_stage", "ephemeral", "operator_env"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := t.Context()
			store := factory(t)
			account, err := store.CreateAccount(ctx, "secret-release@example.test", api.PlanPro)
			mustReleaseSecret(t, err)
			project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "secrets"})
			mustReleaseSecret(t, err)
			app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "secrets-api", WorkloadName: "api", WorkloadClass: WorkloadClassHTTP, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
			mustReleaseSecret(t, err)
			mustReleaseSecret(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "MODE", "old-mode"))
			identity, err := age.GenerateX25519Identity()
			mustReleaseSecret(t, err)
			oldCipher, err := secretbox.SealOne(identity.Recipient(), "TOKEN", "old-private-value", 4096)
			mustReleaseSecret(t, err)
			class := api.SecretClassPersistent
			if outcome == "ephemeral" {
				class = api.SecretClassEphemeral
			}
			mustReleaseSecret(t, store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "production", "TOKEN", identity.Recipient().String(), "aaaaaaaabbbbbbbb", class, oldCipher))
			old, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball})
			mustReleaseSecret(t, err)
			mustReleaseSecret(t, store.MarkDeploymentLive(ctx, old.ID))
			base, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{{app.ID, old.ID}})
			mustReleaseSecret(t, err)
			sealed, err := secretbox.SealOne(identity.Recipient(), "TOKEN", "new-private-value", 4096)
			mustReleaseSecret(t, err)
			stagedRows := []ProjectReleaseSealedSecret{{Workload: "api", AppID: app.ID, Key: "TOKEN", Kid: identity.Recipient().String(), ValueHash: "ccccccccdddddddd", Ciphertext: sealed}}
			secretStore := store.(ProjectReleaseSecretsStore)
			stageID := strings.Repeat("a", 32)
			hash := strings.Repeat("b", 64)
			input := map[string]map[string]string{"api": {"TOKEN": "new-private-value"}}
			staged, err := secretStore.StageProjectReleaseSecrets(ctx, account.ID, project.ID, stageID, hash, stagedRows, input)
			mustReleaseSecret(t, err)
			originalID := staged.Versions["api"]["TOKEN"]
			staged.Versions["api"]["TOKEN"] = "caller-mutated"
			replay, err := secretStore.StageProjectReleaseSecrets(ctx, account.ID, project.ID, stageID, hash, stagedRows, input)
			mustReleaseSecret(t, err)
			if replay.Versions["api"]["TOKEN"] != originalID {
				t.Fatal("staging replay identity changed")
			}
			if _, err := secretStore.StageProjectReleaseSecrets(ctx, account.ID, project.ID, stageID, strings.Repeat("c", 64), stagedRows, input); !errors.Is(err, ErrConflict) {
				t.Fatal("changed stage fingerprint was accepted")
			}
			if outcome == "expired_stage" {
				switch s := store.(type) {
				case *MemStore:
					s.mu.Lock()
					b := s.releaseSecretBundles[account.ID+"\x00"+stageID]
					b.CreatedAt = time.Now().Add(-2 * api.ProjectReleaseSecretStageTTL)
					s.releaseSecretBundles[account.ID+"\x00"+stageID] = b
					s.mu.Unlock()
				case *PgStore:
					tx, err := s.pool.Begin(ctx)
					mustReleaseSecret(t, err)
					defer func() { _ = tx.Rollback(ctx) }()
					_, err = tx.Exec(ctx, "ALTER TABLE project_release_secret_bundles DISABLE TRIGGER immutable_release_secret_envelopes")
					mustReleaseSecret(t, err)
					_, err = tx.Exec(ctx, "UPDATE project_release_secret_bundles SET created_at=now()-interval '2 days' WHERE account_id=$1", account.ID)
					mustReleaseSecret(t, err)
					_, err = tx.Exec(ctx, "ALTER TABLE project_release_secret_bundles ENABLE TRIGGER immutable_release_secret_envelopes")
					mustReleaseSecret(t, err)
					err = tx.Commit(ctx)
					mustReleaseSecret(t, err)
				}
			}
			refs := map[string]map[string]string{"api": {"TOKEN": originalID}}
			if outcome == "wrong_key" {
				refs["api"] = map[string]string{"OTHER": originalID}
			}
			if outcome == "empty" {
				refs["api"] = map[string]string{}
			}
			raw, _ := json.Marshal(api.ProjectReleaseConfigRequest{Env: map[string]map[string]string{"api": {"MODE": "new-mode"}}, Secrets: refs})
			configStore := store.(ProjectReleaseConfigurationStore)
			if outcome == "wrong_owner" {
				other, err := store.CreateAccount(ctx, "other-release@example.test", api.PlanPro)
				mustReleaseSecret(t, err)
				if _, err := configStore.PreviewProjectReleaseConfiguration(ctx, other.ID, project.ID, base.ID, raw); err == nil {
					t.Fatal("foreign account planned release secrets")
				}
				return
			}
			config, err := configStore.PreviewProjectReleaseConfiguration(ctx, account.ID, project.ID, base.ID, raw)
			if outcome == "wrong_key" || outcome == "expired_stage" || outcome == "ephemeral" {
				if err == nil {
					t.Fatal("invalid secret snapshot was planned")
				}
				return
			}
			mustReleaseSecret(t, err)
			if config.Secrets == nil || config.Secrets.PreviousVersions["api"]["TOKEN"] == "" {
				t.Fatal("secret version proof missing")
			}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: base.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Configuration: config, Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/health"}}}}
			attempt := strings.Repeat("d", 32)
			subs := store.(ProjectSubmissionStore)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", SelectionComplete: true, Status: "complete", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			_, err = subs.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("e", 64), receipt)
			mustReleaseSecret(t, err)
			mustReleaseSecret(t, configStore.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, raw))
			mustReleaseSecret(t, configStore.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, raw))
			if err := store.UpsertAppSecretInScope(ctx, account.ID, app.ID, "production", "TOKEN", []byte("forbidden")); !errors.Is(err, ErrConflict) {
				t.Fatalf("mutable secret write bypassed pin: %v", err)
			}
			if err := store.DeleteAppSecretInScope(ctx, account.ID, app.ID, "production", "TOKEN"); !errors.Is(err, ErrConflict) {
				t.Fatalf("mutable secret delete bypassed pin: %v", err)
			}
			candidateCtx := ContextWithProjectReleaseConfigurationAttempt(ContextWithDeploymentSubmission(ctx, ProjectWorkloadAttempt(attempt, app.ID)), attempt)
			started := time.Now().UTC()
			stage, _ := json.Marshal(StageState{Current: StageReadiness, CurrentStartedAt: &started})
			if outcome == "success" {
				if pg, ok := store.(*PgStore); ok {
					_, err := pg.pool.Exec(ctx, `CREATE FUNCTION reject_test_secret_pin() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected secret pin write failure'; END $$; CREATE TRIGGER reject_test_secret_pin BEFORE INSERT ON deployment_release_secret_snapshots FOR EACH ROW EXECUTE FUNCTION reject_test_secret_pin();`)
					mustReleaseSecret(t, err)
					_, err = store.CreateDeployment(candidateCtx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true, StageState: stage})
					if err == nil {
						t.Fatal("injected pin failure was ignored")
					}
					var count int
					mustReleaseSecret(t, pg.pool.QueryRow(ctx, "SELECT count(*) FROM deployments WHERE app_id=$1", app.ID).Scan(&count))
					if count != 1 {
						t.Fatal("pin failure leaked an accepted deployment")
					}
					_, err = pg.pool.Exec(ctx, "DROP TRIGGER reject_test_secret_pin ON deployment_release_secret_snapshots; DROP FUNCTION reject_test_secret_pin()")
					mustReleaseSecret(t, err)
				}
			}
			candidate, err := store.CreateDeployment(candidateCtx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true, StageState: stage})
			mustReleaseSecret(t, err)
			runtime := store.(RuntimeAppValuesStore)
			oldRuntime, err := runtime.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, old.ID)
			mustReleaseSecret(t, err)
			nextRuntime, err := runtime.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, candidate.ID)
			mustReleaseSecret(t, err)
			if !oldRuntime.ReleaseSecretsPinned || !nextRuntime.ReleaseSecretsPinned || !bytes.Equal(oldRuntime.Secrets[0].Ciphertext, oldCipher) {
				t.Fatal("baseline secrets changed before cutover")
			}
			if outcome == "empty" {
				if len(nextRuntime.Secrets) != 0 {
					t.Fatal("explicit empty secret snapshot inherited secrets")
				}
			} else {
				if nextRuntime.ReleaseSecretVersionIDs["TOKEN"] != originalID || !bytes.Equal(nextRuntime.Secrets[0].Ciphertext, sealed) {
					t.Fatal("candidate did not use staged version")
				}
				env, err := secretbox.Open(identity, nextRuntime.Secrets[0].Ciphertext)
				mustReleaseSecret(t, err)
				if env["TOKEN"] != "new-private-value" {
					t.Fatal("candidate encrypted envelope mismatch")
				}
			}
			inputs := RuntimeConfigInputs{Scope: "production", AllSecrets: true, Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}}
			for _, v := range nextRuntime.Values {
				inputs.Variables[v.Key] = v.Value
			}
			for _, v := range nextRuntime.Secrets {
				inputs.SecretVersions["production/"+v.Key] = v.DeliveryVersion
				inputs.SecretRefs[v.Key] = api.SecretRefPrefix + v.Key
			}
			handled, fresh, err := ReleaseSecretRuntimeInputsFresh(ctx, runtime, account.ID, app.ID, candidate.ID, inputs)
			mustReleaseSecret(t, err)
			if !handled || !fresh {
				t.Fatal("pinned receipt was not fresh")
			}
			inputs.Variables["MODE"] = "forged"
			_, fresh, err = ReleaseSecretRuntimeInputsFresh(ctx, runtime, account.ID, app.ID, candidate.ID, inputs)
			mustReleaseSecret(t, err)
			if fresh {
				t.Fatal("forged runtime receipt was accepted")
			}
			if outcome == "candidate_failed" {
				mustReleaseSecret(t, store.UpdateDeploymentStatus(ctx, candidate.ID, DeployFailed, ""))
			} else {
				mustReleaseSecret(t, store.SetDeploymentRootfs(ctx, candidate.ID, "/secret-test.ext4", "layers/"+candidate.ID, 4096))
				mustReleaseSecret(t, store.MarkDeploymentLive(ctx, candidate.ID))
				_, err := store.CloseDeploymentStage(ctx, candidate.ID, StageReadiness, time.Now())
				mustReleaseSecret(t, err)
			}
			if outcome == "operator_env" {
				mustReleaseSecret(t, store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "MODE", "operator"))
			}
			receipt.Apply.Builds = []api.AppliedBuild{{Slug: app.Slug, AppID: app.ID, DeploymentID: candidate.ID, BuildID: "accepted"}}
			mustReleaseSecret(t, subs.WriteProjectSubmission(ctx, account.ID, receipt))
			progress, err := store.(ProjectSafeReleaseStore).ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
			mustReleaseSecret(t, err)
			active, err := store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			mustReleaseSecret(t, err)
			if outcome == "candidate_failed" || outcome == "operator_env" {
				if active.ID != base.ID || (progress.Status != "failed" && progress.Status != "conflicted") {
					t.Fatal("rejected candidate changed production")
				}
				return
			}
			if progress.Status != "verifying" || active.ID != progress.ReleaseID {
				t.Fatalf("secret cutover failed: %+v", progress)
			}
			encoded, _ := json.Marshal(progress)
			for _, private := range []string{"new-private-value", "old-private-value", string(sealed), string(oldCipher)} {
				if strings.Contains(string(encoded), private) {
					t.Fatal("receipt contains secret material")
				}
			}
			if outcome == "rekey_rollback" {
				previous := identity
				identity, err = age.GenerateX25519Identity()
				mustReleaseSecret(t, err)
				rekey := store.(SecretRekeyStore)
				docs, err := rekey.ListSecretRekeyDocuments(ctx, identity.Recipient().String(), "", 100)
				mustReleaseSecret(t, err)
				for _, doc := range docs {
					next, err := TransformSecretRekeyDocument(doc, func(e SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
						values, err := secretbox.Open(previous, e.Ciphertext)
						if err != nil {
							return e, err
						}
						e.Ciphertext, err = secretbox.Seal(identity.Recipient(), values)
						e.Kid = identity.Recipient().String()
						return e, err
					})
					mustReleaseSecret(t, err)
					mustReleaseSecret(t, rekey.CompareAndSwapSecretRekeyDocument(ctx, doc, next))
				}
				mustReleaseSecret(t, rekey.RetireSecretEncryptionKey(ctx, previous.Recipient().String()))
				oldFresh, err := runtime.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, old.ID)
				mustReleaseSecret(t, err)
				oldCipher = oldFresh.Secrets[0].Ciphertext
				values, err := secretbox.Open(identity, oldCipher)
				mustReleaseSecret(t, err)
				if values["TOKEN"] != "old-private-value" {
					t.Fatal("retained baseline cannot open with the new identity")
				}
			}
			ordinary, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true})
			mustReleaseSecret(t, err)
			inherited, err := runtime.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, ordinary.ID)
			mustReleaseSecret(t, err)
			if !inherited.ReleaseSecretsPinned || len(inherited.Secrets) != len(nextRuntime.Secrets) {
				t.Fatal("ordinary deployment lost secret snapshot")
			}
			if outcome == "inherit" {
				if inherited.ReleaseSecretVersionIDs["TOKEN"] != originalID {
					t.Fatal("successor changed secret version")
				}
				return
			}
			recovered, err := subs.ProjectSubmission(ctx, account.ID, attempt)
			mustReleaseSecret(t, err)
			past := time.Now().Add(-2 * time.Minute)
			v := recovered.Apply.SafeRelease.Verification
			v.CheckedAt = &past
			v.HealthyRounds = 1
			if outcome == "rollback" || outcome == "rekey_rollback" {
				expired := time.Now().Add(-time.Second)
				v.Deadline = &expired
			}
			SeedProjectVerificationForTest(t, store, account.ID, recovered)
			result, err := store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, &ProjectReleaseVerificationRound{PreviousCheckedAt: &past, Evidence: []api.ProjectReleaseProbeEvidence{{Probe: v.Probes[0], CandidateRequests: 10, StableRequests: 10}}})
			mustReleaseSecret(t, err)
			if outcome == "rollback" || outcome == "rekey_rollback" {
				result, err = store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, nil)
				mustReleaseSecret(t, err)
				restored, err := store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
				mustReleaseSecret(t, err)
				if result.Status != "rolled_back" || restored.ID != result.RestoredReleaseID || len(restored.Members) != 1 || restored.Members[0].DeploymentID != old.ID {
					t.Fatalf("secret rollback: %+v", result)
				}
			} else if result.Status != "succeeded" {
				t.Fatalf("release did not finish: %+v", result)
			}
			oldAgain, err := runtime.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, old.ID)
			mustReleaseSecret(t, err)
			if !bytes.Equal(oldAgain.Secrets[0].Ciphertext, oldCipher) {
				t.Fatal("rollback baseline secret was mutated")
			}
		})
	}
}
func mustReleaseSecret(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
