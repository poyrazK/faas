package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectWorkerReleaseMemory(t *testing.T) {
	testProjectWorkerRelease(t, func(*testing.T) state.Store { return state.NewMemStore() })
}
func testProjectWorkerRelease(t *testing.T, factory func(*testing.T) state.Store) {
	for _, outcome := range []string{"startup_pending", "startup_failed", "no_config_proof", "cutover", "rollback", "binding_changed", "operator", "worker_lost", "stale_drain", "baseline_mode_changed", "job_low_traffic", "job_rollback", "job_healthy", "job_bad_baseline_deadline"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := t.Context()
			store := factory(t)
			account, err := store.CreateAccount(ctx, "worker-release@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "worker-release"})
			if err != nil {
				t.Fatal(err)
			}
			var apps []state.App
			var oldMembers []state.ProjectReleaseMember
			for _, name := range []string{"web-api", "queue-worker"} {
				class := state.WorkloadClassHTTP
				manifest := state.AppManifest{RevisionPinTTLSeconds: 3600}
				if name == "queue-worker" {
					class = state.WorkloadClassWorker
					manifest.ExecutionMode = api.ExecutionModeWorker
					manifest.StopGracePeriodS = 30
					if outcome == "baseline_mode_changed" {
						manifest.ExecutionMode = ""
					}
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp, WorkloadClass: class, RAMMB: 256, MaxConcurrency: 5, Manifest: manifest})
				if err != nil {
					t.Fatal(err)
				}
				if name == "queue-worker" && outcome == "baseline_mode_changed" {
					if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "production"}); err != nil && !errors.Is(err, state.ErrConflict) {
						t.Fatal(err)
					}
					settings, err := state.WorkloadSettingsFromApp(app)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID, 0, settings); err != nil {
						t.Fatal(err)
					}
				}
				dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:baseline"})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetDeploymentRootfs(ctx, dep.ID, "/baseline.ext4", "layers/"+dep.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
					t.Fatal(err)
				}
				if name == "queue-worker" && outcome == "baseline_mode_changed" {
					manifest.ExecutionMode = api.ExecutionModeWorker
					app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
					if err != nil {
						t.Fatal(err)
					}
				}
				apps = append(apps, app)
				oldMembers = append(oldMembers, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
			}
			baseline, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, oldMembers)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: apps[1].ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			workerStore := store.(state.ProjectReleaseWorkerStore)
			policy, err := workerStore.PreviewProjectReleaseWorker(ctx, account.ID, project.ID, baseline.ID)
			if outcome == "baseline_mode_changed" {
				if !errors.Is(err, state.ErrConflict) {
					t.Fatalf("HTTP baseline admitted as worker rollback target: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if policy.BindingID != binding.Binding.ID || policy.PreviousDeploymentID != oldMembers[1].DeploymentID || len(policy.BindingHash) != 64 {
				t.Fatalf("policy=%+v", policy)
			}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Worker: policy, Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: apps[0].ID, Slug: apps[0].Slug, Method: "GET", Path: "/health"}}}}
			if strings.HasPrefix(outcome, "job_") {
				safe.Verification.WorkerHealth = &api.ProjectWorkerHealth{Status: "insufficient_evidence"}
			}
			attempt := strings.Repeat("c", 32)
			receipts := store.(state.ProjectSubmissionStore)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", Status: "processing", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			if _, err := receipts.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("d", 64), receipt); err != nil {
				t.Fatal(err)
			}
			var workerCandidate state.Deployment
			for i, app := range apps {
				now := time.Now()
				stages, _ := json.Marshal(state.StageState{Current: state.StageReadiness, CurrentStartedAt: &now})
				creationCtx := state.ContextWithDeploymentSubmission(ctx, state.ProjectWorkloadAttempt(attempt, app.ID))
				if i == 1 {
					creationCtx = state.ContextWithProjectReleaseWorkerAttempt(creationCtx, attempt)
				}
				dep, err := store.CreateDeployment(creationCtx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:candidate", TrafficPercentExplicit: true, StageState: stages})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetDeploymentRootfs(ctx, dep.ID, "/candidate.ext4", "layers/"+dep.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.CloseDeploymentStage(ctx, dep.ID, state.StageReadiness, time.Now()); err != nil {
					t.Fatal(err)
				}
				if i == 1 {
					workerCandidate = dep
				}
				receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{AppID: app.ID, Slug: app.Slug, DeploymentID: dep.ID, BuildID: "build"})
			}
			receipt.Status = "complete"
			receipt.SelectionComplete = true
			if err := receipts.WriteProjectSubmission(ctx, account.ID, receipt); err != nil {
				t.Fatal(err)
			}
			held, err := workerStore.HeldProjectReleaseWorkers(ctx, apps[1].ID)
			if err != nil || len(held) != 1 || held[0] != workerCandidate.ID {
				t.Fatalf("candidate not isolated: %v %v", held, err)
			}
			queued, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: apps[1].ID, AccountID: account.ID, DeploymentScope: "production", Source: state.InvocationQueue, QueueBindingID: binding.Binding.ID, QueueName: "orders", Method: "POST", Path: "/", Payload: json.RawMessage(`{"order":"before-cutover"}`), DueAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			before, version, err := state.ResolveInvocationVersion(ctx, store, queued)
			if err != nil || version.DeploymentID != oldMembers[1].DeploymentID {
				t.Fatalf("candidate received production work: %+v %v", version, err)
			}
			_ = before
			if outcome == "startup_failed" {
				if err := store.UpdateDeploymentStatus(ctx, workerCandidate.ID, state.DeployFailed, "startup failed"); err != nil {
					t.Fatal(err)
				}
			}
			var instance, oldInstance state.Instance
			if outcome != "startup_pending" && outcome != "startup_failed" {
				node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "worker-release-node", TargetURL: "unix:///tmp/worker-release.sock", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Lifecycle: state.NodeLifecycleActive})
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "stale_drain" {
					oldInstance, err = store.CreateInstanceWithMode(ctx, apps[1].ID, oldMembers[1].DeploymentID, string(state.StateColdBooting), 256, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
					if err != nil {
						t.Fatal(err)
					}
					if err := store.UpdateInstanceState(ctx, oldInstance.ID, string(state.StateRunning)); err != nil {
						t.Fatal(err)
					}
				}
				instance, err = store.CreateInstanceWithMode(ctx, apps[1].ID, workerCandidate.ID, string(state.StateColdBooting), 256, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "no_config_proof" {
					if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateRunning)); err != nil {
						t.Fatal(err)
					}
				} else {
					snapshot, err := store.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, account.ID, apps[1].ID, workerCandidate.ID)
					if err != nil {
						t.Fatal(err)
					}
					fence, err := state.NewRuntimeAppConfigFence(snapshot)
					if err != nil {
						t.Fatal(err)
					}
					inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
					instance, err = store.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: account.ID, AppID: apps[1].ID, InstanceID: instance.ID, NodeID: node.ID, WakeID: instance.WakeID, ExpectedState: instance.State, Netns: "fc-" + instance.ID, HostIP: "10.100.0.8", GuestUID: 20008, Fence: fence.SecretFence, ConfigFence: fence, Inputs: &inputs})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if outcome == "stale_drain" {
				claimed, err := workerStore.ClaimProjectWorkerDrain(ctx, apps[1].ID, instance.ID, oldMembers[1].DeploymentID)
				if err != nil || claimed {
					t.Fatalf("held qualification worker was drained: %v %v", claimed, err)
				}
			}
			if outcome == "binding_changed" {
				disabled := false
				if _, err := store.(state.QueueBindingConsumerStore).UpdateQueueBindingWithConsumer(ctx, account.ID, apps[1].ID, binding.Binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled}); err != nil {
					t.Fatal(err)
				}
			}
			progress, err := store.(state.ProjectSafeReleaseStore).ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			expected := "verifying"
			if outcome == "startup_pending" || outcome == "no_config_proof" {
				expected = "building"
			}
			if outcome == "startup_failed" {
				expected = "failed"
			}
			if outcome == "binding_changed" {
				expected = "conflicted"
			}
			if progress.Status != expected {
				t.Fatalf("progress=%+v worker=%+v", progress, progress.Worker)
			}
			if expected != "verifying" {
				_, selected, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: apps[1].ID, AccountID: account.ID, DeploymentScope: "production", Source: state.InvocationQueue})
				if err != nil || selected.DeploymentID != oldMembers[1].DeploymentID {
					t.Fatalf("failed qualification changed consumer: %+v %v", selected, err)
				}
				return
			}
			_, selected, err := state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: apps[1].ID, AccountID: account.ID, DeploymentScope: "production", Source: state.InvocationQueue})
			if err != nil || selected.DeploymentID != workerCandidate.ID {
				t.Fatalf("cutover didn't switch worker admission: %+v %v", selected, err)
			}
			held, err = workerStore.HeldProjectReleaseWorkers(ctx, apps[1].ID)
			if err != nil || len(held) != 0 {
				t.Fatalf("active worker held: %+v %v", held, err)
			}
			if outcome == "stale_drain" {
				claimed, err := workerStore.ClaimProjectWorkerDrain(ctx, apps[1].ID, instance.ID, oldMembers[1].DeploymentID)
				if err != nil || claimed {
					t.Fatalf("stale reconciler drained active worker: %v %v", claimed, err)
				}
				claimed, err = workerStore.ClaimProjectWorkerDrain(ctx, apps[1].ID, oldInstance.ID, oldMembers[1].DeploymentID)
				if err != nil || claimed {
					t.Fatalf("stale target acquired retirement: %v %v", claimed, err)
				}
				claimed, err = workerStore.ClaimProjectWorkerDrain(ctx, apps[1].ID, oldInstance.ID, workerCandidate.ID)
				if err != nil || !claimed {
					t.Fatalf("correct target could not retire old worker: %v %v", claimed, err)
				}
				if _, err := store.CreateInstanceWithMode(ctx, apps[1].ID, oldMembers[1].DeploymentID, string(state.StateColdBooting), 256, instance.NodeID, uuid.NewString(), string(state.InstanceModeWorker)); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("stale old admission succeeded: %v", err)
				}
			}
			if outcome == "operator" {
				if _, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, oldMembers); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "worker_lost" {
				if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateFailed)); err != nil {
					t.Fatal(err)
				}
			}
			saved, err := receipts.ProjectSubmission(ctx, account.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "rollback" {
				past := time.Now().Add(-time.Second)
				saved.Apply.SafeRelease.Verification.Deadline = &past
				state.SeedProjectVerificationForTest(t, store, account.ID, saved)
			}
			proof := api.ProjectReleaseProbeEvidence{Probe: safe.Verification.Probes[0], CandidateRequests: 10, StableRequests: 10}
			if strings.HasPrefix(outcome, "job_") {
				cutover := time.Now().Add(-2 * time.Minute)
				if outcome == "job_bad_baseline_deadline" {
					cutover = time.Now().Add(-11 * time.Minute)
				}
				deadline := cutover.Add(api.ProjectReleaseVerificationTimeout)
				saved.Apply.SafeRelease.Verification.Deadline = &deadline
				state.SeedProjectVerificationForTest(t, store, account.ID, saved)
				if outcome != "job_low_traffic" {
					state.SeedProjectWorkerJobSamplesForTest(t, store, apps[1].ID, account.ID, binding.Binding.ID, oldMembers[1].DeploymentID, cutover.Add(-time.Second), outcome == "job_bad_baseline_deadline")
					state.SeedProjectWorkerJobSamplesForTest(t, store, apps[1].ID, account.ID, binding.Binding.ID, workerCandidate.ID, cutover.Add(time.Second), outcome == "job_rollback")
				}
				if outcome == "job_bad_baseline_deadline" {
					result, e := store.(state.ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, nil)
					if e != nil || result.Status != "rollback_failed" {
						t.Fatalf("deadline restored a failing worker baseline: %+v %v", result, e)
					}
					_, selected, e := state.ResolveInvocationVersion(ctx, store, queued)
					if e != nil || selected.DeploymentID != workerCandidate.ID {
						t.Fatal("deadline overwrote graph despite failing baseline")
					}
					return
				}
				first, e := store.(state.ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, &state.ProjectReleaseVerificationRound{Evidence: []api.ProjectReleaseProbeEvidence{proof}})
				if e != nil || first.Status != "verifying" {
					t.Fatalf("first job round: %+v %v", first, e)
				}
				if outcome == "job_rollback" && first.Verification.WorkerHealth.Candidate.Retries != api.ProjectWorkerHealthMinAttempts {
					t.Fatalf("retry outcomes not attributed: %+v", first.Verification.WorkerHealth.Candidate)
				}
				saved, e = receipts.ProjectSubmission(ctx, account.ID, attempt)
				if e != nil {
					t.Fatal(e)
				}
				previous := time.Now().Add(-61 * time.Second)
				saved.Apply.SafeRelease.Verification.CheckedAt = &previous
				state.SeedProjectVerificationForTest(t, store, account.ID, saved)
				if outcome != "job_low_traffic" {
					state.SeedProjectWorkerJobSamplesForTest(t, store, apps[1].ID, account.ID, binding.Binding.ID, workerCandidate.ID, time.Now().Add(-time.Second), outcome == "job_rollback")
				}
				second, e := store.(state.ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, &state.ProjectReleaseVerificationRound{PreviousCheckedAt: &previous, Evidence: []api.ProjectReleaseProbeEvidence{proof}})
				if e != nil {
					t.Fatal(e)
				}
				want := "verifying"
				if outcome == "job_rollback" {
					want = "rolling_back"
				}
				if outcome == "job_healthy" {
					want = "succeeded"
				}
				if second.Status != want {
					t.Fatalf("worker job result: %+v health=%+v want=%s", second, second.Verification.WorkerHealth, want)
				}
				if outcome == "job_rollback" {
					restored, e := store.(state.ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, nil)
					if e != nil || restored.Status != "rolled_back" {
						t.Fatalf("job rollback: %+v %v", restored, e)
					}
					_, selected, e := state.ResolveInvocationVersion(ctx, store, queued)
					if e != nil || selected.DeploymentID != oldMembers[1].DeploymentID {
						t.Fatalf("worker admission not restored: %+v %v", selected, e)
					}
				}
				return
			}
			next, err := store.(state.ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, &state.ProjectReleaseVerificationRound{Evidence: []api.ProjectReleaseProbeEvidence{proof}})
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "worker_lost" && next.Verification.HealthyRounds != 0 {
				t.Fatal("healthy HTTP hid worker failure")
			}
			if outcome == "operator" && next.Status != "conflicted" {
				t.Fatalf("operator graph overwritten: %+v", next)
			}
			if outcome == "rollback" {
				next, err = store.(state.ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, nil)
				if err != nil || next.Status != "rolled_back" || next.Worker.Status != "restored" {
					t.Fatalf("rollback=%+v %v", next, err)
				}
				_, selected, err = state.ResolveInvocationVersion(ctx, store, state.Invocation{AppID: apps[1].ID, AccountID: account.ID, DeploymentScope: "production", Source: state.InvocationQueue})
				if err != nil || selected.DeploymentID != oldMembers[1].DeploymentID {
					t.Fatalf("rollback lost worker: %+v %v", selected, err)
				}
			}
			if _, err := store.CreateDeployment(state.ContextWithProjectReleaseWorkerAttempt(ctx, attempt), state.Deployment{AppID: apps[1].ID, Scope: "production", Kind: state.DeploymentKindTarball, TrafficPercentExplicit: true}); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("untracked candidate admitted: %v", err)
			}
		})
	}
}
