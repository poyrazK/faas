package state_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectReleaseConfigurationMemory(t *testing.T) {
	testProjectReleaseConfiguration(t, func(*testing.T) state.Store { return state.NewMemStore() })
}
func testProjectReleaseConfiguration(t *testing.T, newStore func(*testing.T) state.Store) {
	t.Helper()
	for _, outcome := range []string{"success", "rollback", "empty", "candidate_failed", "config_before_cutover", "env_before_cutover", "operator_verifying", "operator_rollback", "operator_aba", "inherit"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := t.Context()
			store := newStore(t)
			account, err := store.CreateAccount(ctx, "paired@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "paired"})
			if err != nil {
				t.Fatal(err)
			}
			var apps []state.App
			var oldMembers []state.ProjectReleaseMember
			for _, name := range []string{"api", "web"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "paired-" + name, WorkloadName: name, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "MODE", "previous-value-not-in-receipts"); err != nil {
					t.Fatal(err)
				}
				if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "REMOVED", "old-extra"); err != nil {
					t.Fatal(err)
				}
				old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
					t.Fatal(err)
				}
				apps = append(apps, app)
				oldMembers = append(oldMembers, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: old.ID})
			}
			publisher := store.(state.ProjectReleaseSetStore)
			reader := store.(state.ProjectReleaseSetReader)
			configStore := store.(state.ProjectReleaseConfigurationStore)
			submissions := store.(state.ProjectSubmissionStore)
			meta, _ := json.Marshal(map[string]string{"region": "eu"})
			canonical, hash, _ := api.NormalizeProjectEnvironmentConfig(meta)
			previous, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production", Values: canonical, ConfigHash: hash})
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, oldMembers)
			if err != nil {
				t.Fatal(err)
			}
			request := json.RawMessage(`{"env":{"api":{"MODE":"proposed-value-not-in-receipts"},"web":{"MODE":"web-next"}}}`)
			if outcome == "empty" {
				request = json.RawMessage(`{"env":{"api":{},"web":{"MODE":"web-next"}}}`)
			}
			configuration, err := configStore.PreviewProjectReleaseConfiguration(ctx, account.ID, project.ID, baseline.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Configuration: configuration, Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: apps[0].ID, Slug: apps[0].Slug, Method: "GET", Path: "/health"}}}}
			attempt := strings.Repeat("d", 32)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", SelectionComplete: true, Status: "complete", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			if _, err := submissions.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("e", 64), receipt); err != nil {
				t.Fatal(err)
			}
			if err := configStore.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, request); err != nil {
				t.Fatal(err)
			}
			if err := configStore.StageProjectReleaseConfiguration(ctx, account.ID, attempt, project.ID, safe, request); err != nil {
				t.Fatalf("duplicate stage: %v", err)
			}
			values := store.(state.RuntimeAppValuesStore)
			baselineBefore, err := values.RuntimeAppValuesForDeployment(ctx, account.ID, apps[0].ID, oldMembers[0].DeploymentID)
			if err != nil {
				t.Fatal(err)
			}
			oldFence, err := state.NewRuntimeAppConfigFence(baselineBefore)
			if err != nil {
				t.Fatal(err)
			}
			for i, app := range apps {
				now := time.Now()
				stage, _ := json.Marshal(state.StageState{Current: state.StageReadiness, CurrentStartedAt: &now})
				candidateCtx := state.ContextWithDeploymentSubmission(ctx, state.ProjectWorkloadAttempt(attempt, app.ID))
				candidateCtx = state.ContextWithProjectReleaseConfigurationAttempt(candidateCtx, attempt)
				candidate, err := store.CreateDeployment(candidateCtx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, TrafficPercentExplicit: true, StageState: stage})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.CloseDeploymentStage(ctx, candidate.ID, state.StageReadiness, time.Now()); err != nil {
					t.Fatal(err)
				}
				if outcome == "candidate_failed" && i == 1 {
					if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployFailed, ""); err != nil {
						t.Fatal(err)
					}
				}
				receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{Slug: app.Slug, AppID: app.ID, DeploymentID: candidate.ID, BuildID: "accepted"})
				if i == 0 {
					snapshot, err := values.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, candidate.ID)
					if err != nil {
						t.Fatal(err)
					}
					if outcome == "empty" {
						if len(snapshot.Values) != 0 {
							t.Fatal("empty configuration inherited legacy variables")
						}
					} else if len(snapshot.Values) != 1 || snapshot.Values[0].Key != "MODE" || snapshot.Values[0].Value != "proposed-value-not-in-receipts" {
						t.Fatalf("candidate boot inputs=%+v", snapshot.Values)
					}
				}
			}
			current, err := store.ProjectEnvironmentConfigLatest(ctx, account.ID, project.ID, "production")
			if err != nil || current.Version != previous.Version {
				t.Fatalf("configuration changed before cutover: %+v %v", current, err)
			}
			if err := submissions.WriteProjectSubmission(ctx, account.ID, receipt); err != nil {
				t.Fatal(err)
			}
			if outcome == "config_before_cutover" {
				writeReleaseMetadata(t, store, account.ID, project.ID, json.RawMessage(`{"region":"operator"}`))
			}
			if outcome == "env_before_cutover" {
				if err := store.UpsertAppEnvInScope(ctx, account.ID, apps[0].ID, "production", "MODE", "operator-value"); err != nil {
					t.Fatal(err)
				}
			}
			progress, err := store.(state.ProjectSafeReleaseStore).ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			active, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "candidate_failed" || outcome == "config_before_cutover" || outcome == "env_before_cutover" {
				wanted := "conflicted"
				if outcome == "candidate_failed" {
					wanted = "failed"
				}
				if progress.Status != wanted || active.ID != baseline.ID {
					t.Fatalf("early failure switched graph: %+v %s", progress, active.ID)
				}
				current, _ = store.ProjectEnvironmentConfigLatest(ctx, account.ID, project.ID, "production")
				expectedVersion := previous.Version
				if outcome == "config_before_cutover" {
					expectedVersion++
				}
				if current.Version != expectedVersion {
					t.Fatal("refused cutover changed configuration head")
				}
				return
			}
			if progress.Status != "verifying" || active.ID != progress.ReleaseID || progress.Configuration.TargetVersion != previous.Version+1 {
				t.Fatalf("paired cutover=%+v", progress)
			}
			current, err = store.ProjectEnvironmentConfigLatest(ctx, account.ID, project.ID, "production")
			if err != nil || current.ConfigHash != progress.Configuration.TargetHash || current.Version != progress.Configuration.TargetVersion {
				t.Fatalf("code/config cutover disagrees: %+v %v", current, err)
			}
			oldRuntime, err := values.RuntimeAppValuesForDeployment(ctx, account.ID, apps[0].ID, oldMembers[0].DeploymentID)
			if err != nil {
				t.Fatal(err)
			}
			oldAfterFence, err := state.NewRuntimeAppConfigFence(oldRuntime)
			if err != nil || oldAfterFence != oldFence || len(oldRuntime.Values) != 2 || oldRuntime.Values[0].Value != "previous-value-not-in-receipts" {
				t.Fatalf("baseline config changed after cutover: %+v %v", oldRuntime.Values, err)
			}
			recovered, err := submissions.ProjectSubmission(ctx, account.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(recovered)
			if strings.Contains(string(encoded), "proposed-value-not-in-receipts") || strings.Contains(string(encoded), "previous-value-not-in-receipts") {
				t.Fatal("configuration plaintext leaked into public receipt")
			}
			if outcome == "inherit" {
				ordinary, err := store.CreateDeployment(ctx, state.Deployment{AppID: apps[0].ID, Scope: "production", Kind: state.DeploymentKindTarball, TrafficPercentExplicit: true})
				if err != nil {
					t.Fatal(err)
				}
				ordinaryRuntime, err := values.RuntimeAppValuesForDeployment(ctx, account.ID, apps[0].ID, ordinary.ID)
				if err != nil || len(ordinaryRuntime.Values) != 1 || ordinaryRuntime.Values[0].Value != "proposed-value-not-in-receipts" {
					t.Fatalf("ordinary successor lost config: %+v %v", ordinaryRuntime.Values, err)
				}
				inherited, err := configStore.PreviewProjectReleaseConfiguration(ctx, account.ID, project.ID, active.ID, nil)
				if err != nil || inherited == nil || inherited.RequestHash != "" || inherited.TargetHash != current.ConfigHash {
					t.Fatalf("missing configuration inheritance: %+v %v", inherited, err)
				}
				nextAttempt := strings.Repeat("a", 32)
				nextSafe := *progress
				nextSafe.Status = "building"
				nextSafe.PreviousReleaseID = active.ID
				nextSafe.Configuration = inherited
				nextReceipt := receipt
				nextReceipt.AttemptID = nextAttempt
				nextReceipt.Apply.SafeRelease = &nextSafe
				if _, err := submissions.ReserveProjectSubmission(ctx, account.ID, nextAttempt, strings.Repeat("b", 64), nextReceipt); err != nil {
					t.Fatal(err)
				}
				if err := configStore.StageProjectReleaseConfiguration(ctx, account.ID, nextAttempt, project.ID, &nextSafe, nil); err != nil {
					t.Fatal(err)
				}
				candidateCtx := state.ContextWithProjectReleaseConfigurationAttempt(state.ContextWithDeploymentSubmission(ctx, state.ProjectWorkloadAttempt(nextAttempt, apps[0].ID)), nextAttempt)
				next, err := store.CreateDeployment(candidateCtx, state.Deployment{AppID: apps[0].ID, Scope: "production", Kind: state.DeploymentKindTarball, TrafficPercentExplicit: true})
				if err != nil {
					t.Fatal(err)
				}
				runtime, err := values.RuntimeAppValuesForDeployment(ctx, account.ID, apps[0].ID, next.ID)
				if err != nil || len(runtime.Values) != 1 || runtime.Values[0].Value != "proposed-value-not-in-receipts" {
					t.Fatalf("source-only successor lost config: %+v %v", runtime.Values, err)
				}
				return
			}
			verifier := store.(state.ProjectReleaseVerificationStore)
			v := recovered.Apply.SafeRelease.Verification
			past := time.Now().Add(-2 * time.Minute)
			v.CheckedAt = &past
			v.HealthyRounds = 1
			if outcome == "rollback" || outcome == "operator_rollback" {
				expired := time.Now().Add(-time.Second)
				v.Deadline = &expired
			}
			state.SeedProjectVerificationForTest(t, store, account.ID, recovered)
			if outcome == "operator_verifying" {
				writeReleaseMetadata(t, store, account.ID, project.ID, json.RawMessage(`{"region":"operator"}`))
			}
			if outcome == "operator_aba" {
				writeReleaseMetadata(t, store, account.ID, project.ID, current.Values)
			}
			round := &state.ProjectReleaseVerificationRound{PreviousCheckedAt: &past, Evidence: []api.ProjectReleaseProbeEvidence{{Probe: v.Probes[0], CandidateRequests: 10, StableRequests: 10}}}
			result, err := verifier.AdvanceProjectReleaseVerification(ctx, account.ID, attempt, round)
			if err != nil {
				t.Fatal(err)
			}
			expected := "succeeded"
			if outcome == "operator_verifying" || outcome == "operator_aba" {
				expected = "conflicted"
			}
			expectedActive := active.ID
			if outcome == "rollback" || outcome == "operator_rollback" {
				if result.Status != "rolling_back" {
					t.Fatalf("missing durable rollback: %+v", result)
				}
				if outcome == "operator_rollback" {
					writeReleaseMetadata(t, store, account.ID, project.ID, json.RawMessage(`{"region":"operator"}`))
				}
				result, err = verifier.AdvanceProjectReleaseVerification(ctx, account.ID, attempt, nil)
				if err != nil {
					t.Fatal(err)
				}
				expected = "rolled_back"
				if outcome == "operator_rollback" {
					expected = "conflicted"
				} else {
					expectedActive = result.RestoredReleaseID
				}
			}
			if result.Status != expected {
				t.Fatalf("result=%s want=%s", result.Status, expected)
			}
			active, err = reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			if err != nil || active.ID != expectedActive {
				t.Fatalf("wrong graph after verification: %+v %v", active, err)
			}
			current, _ = store.ProjectEnvironmentConfigLatest(ctx, account.ID, project.ID, "production")
			if expected == "rolled_back" {
				if current.ConfigHash != previous.ConfigHash || current.Version != previous.Version+2 || result.Configuration.RestoredVersion != current.Version {
					t.Fatalf("rollback restored code without config: %+v %+v", result, current)
				}
				for _, member := range oldMembers {
					_, id, err := publisher.ResolveProjectRelease(ctx, member.AppID, "production", "")
					if err != nil || id != member.DeploymentID {
						t.Fatal("rollback graph mixed")
					}
				}
				runtime, err := values.RuntimeAppValuesForDeployment(ctx, account.ID, apps[0].ID, oldMembers[0].DeploymentID)
				if err != nil || runtime.Values[0].Value != "previous-value-not-in-receipts" {
					t.Fatal("restored code received candidate variables")
				}
			}
			again, err := verifier.AdvanceProjectReleaseVerification(ctx, account.ID, attempt, nil)
			final, _ := store.ProjectEnvironmentConfigLatest(ctx, account.ID, project.ID, "production")
			if err != nil || again.Status != result.Status || again.RestoredReleaseID != result.RestoredReleaseID || final.Version != current.Version {
				t.Fatal("replayed verification changed graph or config version")
			}
		})
	}
}
func writeReleaseMetadata(t *testing.T, store state.Store, accountID, projectID string, raw json.RawMessage) {
	t.Helper()
	canonical, hash, err := api.NormalizeProjectEnvironmentConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(t.Context(), state.ProjectEnvironmentConfig{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: "production", Values: canonical, ConfigHash: hash}); err != nil {
		t.Fatal(err)
	}
}
