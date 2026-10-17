package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// Signed byte and execution-receipt fixtures validate the control plane, not KVM.
func TestGitOpsScheduledArtifactPromotionAndRollbackRequireFreshOccurrence(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, kind := range []string{"source", "dockerfile"} {
			t.Run(provider+"/"+kind, func(t *testing.T) {
				buildArtifactStores(t, func(t *testing.T, basic gitOpsTestStore, pool *pgxpool.Pool) {
					ctx := t.Context()
					lease, desired, intent, plan := sourceScheduledJobFixture(t, basic, provider, kind, true)
					prep := basic.(state.EnvironmentGitOpsPreparationStore)
					requests, err := prep.EnvironmentGitOpsSourceRequests(ctx, lease, plan)
					if err != nil || len(requests) != 1 {
						t.Fatal("origin request", err)
					}
					candidates, err := prep.PrepareEnvironmentGitOpsCandidates(ctx, lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: sourceArtifact(t, requests[0])})
					if err != nil || len(candidates) != 1 {
						t.Fatal("origin candidate", err)
					}
					origin, err := basic.DeploymentByID(ctx, candidates[0].DeploymentID)
					if err != nil {
						t.Fatal(err)
					}
					root, _ := publishReuseSourceArtifact(t, basic, origin)
					originRelease := activateAndServeArtifactJob(t, basic, lease, plan, intent, origin.ID, root.Input.StorageKey)
					app, err := basic.AppByID(ctx, intent.AppID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = basic.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: app.AccountID, ProjectID: app.ProjectID, Slug: "staging"}); err != nil {
						t.Fatal(err)
					}
					spec := lease.Source.Spec
					spec.ManifestPath = "staging.yaml"
					targetSource, err := basic.CreateEnvironmentGitSource(ctx, app.AccountID, app.ProjectID, "staging", spec)
					if err != nil {
						t.Fatal(err)
					}
					// Compile clones the definition; target command, schedule and values differ.
					target, err := environmentsync.Compile(desired.Definition)
					if err != nil {
						t.Fatal(err)
					}
					target.Definition.Environment = "staging"
					w := target.Definition.Workloads["report"]
					w.Runtime = json.RawMessage(`{"execution_mode":"job","entrypoint":["/usr/local/bin/node","/app/report.js","target"]}`)
					w.Schedule = &api.EnvironmentJobSchedule{Cron: "0 3 * * *", Timezone: "Europe/Istanbul"}
					w.Variables = map[string]string{"MODE": "TARGET_PRIVATE"}
					target.Definition.Workloads["report"] = w
					target, err = environmentsync.Compile(target.Definition)
					if err != nil {
						t.Fatal(err)
					}
					targetSource, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(targetSource, target, strings.Repeat("e", 40)))
					if err != nil {
						t.Fatal(err)
					}
					adoptWorkloadIntent(t, basic.(intentTestStore), targetSource)
					pollArtifactJobSources(t, basic, map[string]environmentsync.DesiredState{lease.Source.ID: desired, targetSource.ID: target}, map[string]string{lease.Source.ID: lease.Revision.CommitSHA, targetSource.ID: strings.Repeat("e", 40)})
					from, err := basic.EnvironmentGitSource(ctx, app.AccountID, app.ProjectID, "production")
					if err != nil {
						t.Fatal(err)
					}
					targetSource, err = basic.EnvironmentGitSource(ctx, app.AccountID, app.ProjectID, "staging")
					if err != nil {
						t.Fatal(err)
					}
					preview, err := basic.(state.EnvironmentGitOpsPromotionStore).PreviewEnvironmentGitOpsPromotion(ctx, from, targetSource, api.PreviewEnvironmentGitPromotionRequest{FromEnvironment: "production", TargetCommitSHA: strings.Repeat("e", 40)}, target)
					if err != nil || len(preview.BlockingReasons) != 0 || preview.Origin.ReleaseSetID != originRelease.ID {
						currentEvidence, _ := basic.(state.EnvironmentGitOpsCurrentWorkloadEvidenceStore).CurrentEnvironmentGitOpsWorkloadEvidence(ctx, app.AccountID, from.ID)
						leaseEvidence, _ := basic.(state.EnvironmentGitOpsActivationEvidenceStore).EnvironmentGitOpsActivationEvidence(ctx, lease, plan)
						t.Fatalf("promotion %v %v; current=%+v lease=%+v", preview.BlockingReasons, err, currentEvidence, leaseEvidence)
					}
					raw, err := promotion.ExportManifest(preview, target.Definition)
					if err != nil {
						t.Fatal(err)
					}
					var promoted api.EnvironmentDefinition
					if err = json.Unmarshal(raw, &promoted); err != nil {
						t.Fatal(err)
					}
					target, err = environmentsync.Compile(promoted)
					if err != nil {
						t.Fatal(err)
					}
					if target.Definition.Workloads["report"].Source.BuildArtifactID != root.ID {
						t.Fatal("promotion selected different bytes")
					}
					targetSource, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(targetSource, target, strings.Repeat("f", 40)))
					if err != nil {
						t.Fatal("target approval", err)
					}
					adoptWorkloadIntent(t, basic.(intentTestStore), targetSource)
					targetLease, err := basic.ClaimEnvironmentGitOps(ctx, "job-artifact-target", time.Now(), 5*time.Minute)
					if err != nil || targetLease.Source.ID != targetSource.ID {
						t.Fatal("target lease", err)
					}
					store := basic.(intentTestStore)
					if _, err = store.ApplyEnvironmentGitOps(ctx, targetLease, claimedIntentPlan(t, store, targetLease, target)); err != nil {
						t.Fatal("target intent", err)
					}
					targetPlan := claimedIntentPlan(t, store, targetLease, target)
					targetIntent, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(ctx, app.AccountID, app.ID, targetSource.EnvironmentID)
					if err != nil || targetIntent.SourceRevision != "" {
						t.Fatal("target code identity", err)
					}
					before, err := basic.ListBuildsForAccount(ctx, app.AccountID)
					if err != nil {
						t.Fatal(err)
					}
					requests, err = prep.EnvironmentGitOpsSourceRequests(ctx, targetLease, targetPlan)
					if err != nil || len(requests) != 0 {
						t.Fatal("reuse requested archive", err)
					}
					candidates, err = prep.PrepareEnvironmentGitOpsCandidates(ctx, targetLease, targetPlan, nil)
					if err != nil || len(candidates) != 1 {
						t.Fatal("reuse candidate", err)
					}
					dep, err := basic.DeploymentByID(ctx, candidates[0].DeploymentID)
					if err != nil {
						t.Fatal(err)
					}
					frozen, err := dep.ScopedWorkloadRuntime()
					if err != nil || frozen.BuiltArtifact == nil || frozen.BuiltArtifact.CommitSHA != origin.CommitSHA || frozen.SourceArchive != nil || dep.BuildID != "" || dep.RootfsKey != root.Input.StorageKey || !dep.EnvironmentWorkloadHeld() || frozen.Variables["MODE"] != "TARGET_PRIVATE" {
						t.Fatal("borrowed artifact/config", err)
					}
					job, err := basic.JobGetByID(ctx, targetIntent.JobID)
					if err != nil || job.Status != "paused" || !strings.Contains(job.ImageRef, "/artifact/"+root.ID+"/"+root.InputHash) || job.CronSchedule != "0 3 * * *" || job.CronTimezone != "Europe/Istanbul" {
						t.Fatal("paused target binding", err)
					}
					if pool != nil {
						var ref string
						if err = pool.QueryRow(ctx, `SELECT environment_workload_source_job_reference($1::uuid,$2::uuid,$3::jsonb,NULL)`, targetIntent.EnvironmentID, app.ID, mustJSONArtifactJob(t, targetIntent.Source)).Scan(&ref); err != nil || ref != job.ImageRef {
							t.Fatal("SQL/Go identity", err)
						}
						checkArtifactJobPredicateAndWriterFence(t, pool, root.ID, dep.ID)
					}
					targetRelease := activateAndServeArtifactJob(t, basic, targetLease, targetPlan, targetIntent, dep.ID, root.Input.StorageKey)
					after, err := basic.ListBuildsForAccount(ctx, app.AccountID)
					if err != nil || len(after) != len(before) {
						t.Fatal("promotion enqueued build", err)
					}
					// A rollback proposal selects code for future executions and preserves
					// the latest target command/schedule. Neither listing nor preview runs it.
					current, err := basic.EnvironmentGitSource(ctx, app.AccountID, app.ProjectID, "staging")
					if err != nil {
						t.Fatal(err)
					}
					next, err := environmentsync.Compile(target.Definition)
					if err != nil {
						t.Fatal(err)
					}
					w = next.Definition.Workloads["report"]
					w.Source = &api.EnvironmentWorkloadSource{Kind: kind, Directory: "jobs/report"}
					w.Schedule = &api.EnvironmentJobSchedule{Cron: "30 4 * * *", Timezone: "UTC"}
					next.Definition.Workloads["report"] = w
					next, err = environmentsync.Compile(next.Definition)
					if err != nil {
						t.Fatal(err)
					}
					request := api.PreviewEnvironmentGitRollbackRequest{ReleaseSetID: targetRelease.ID, TargetCommitSHA: strings.Repeat("d", 40)}
					rollbackPlan, err := basic.(state.EnvironmentGitOpsRollbackStore).PreviewEnvironmentGitOpsRollback(ctx, current, request, next)
					if err != nil || len(rollbackPlan.BlockingReasons) != 0 {
						t.Fatal("scheduled rollback", rollbackPlan.BlockingReasons, err)
					}
					raw, err = rollback.ExportManifest(rollbackPlan, next.Definition)
					if err != nil {
						t.Fatal(err)
					}
					var recovered api.EnvironmentDefinition
					if err = json.Unmarshal(raw, &recovered); err != nil {
						t.Fatal(err)
					}
					if recovered.Workloads["report"].Source.BuildArtifactID != root.ID || recovered.Workloads["report"].Schedule.Cron != "30 4 * * *" || recovered.Workloads["report"].Variables["MODE"] != "TARGET_PRIVATE" {
						t.Fatal("rollback replaced target execution settings")
					}
					catalog, err := basic.(state.EnvironmentGitOpsRecoveryStore).ListEnvironmentGitReleases(ctx, current, api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: request.TargetCommitSHA}, next)
					if err != nil || len(catalog.Releases) != 1 || catalog.Releases[0].Eligibility != "eligible" || catalog.Releases[0].PlanHash != rollbackPlan.PlanHash {
						t.Fatal("scheduled recovery catalog", err)
					}
					recovery, err := environmentsync.Compile(recovered)
					if err != nil {
						t.Fatal(err)
					}
					current, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(current, recovery, request.TargetCommitSHA))
					if err != nil {
						t.Fatal("reviewed rollback approval", err)
					}
					adoptWorkloadIntent(t, store, current)
					recoveryLease, err := basic.ClaimEnvironmentGitOps(ctx, "job-artifact-recovery", time.Now(), 5*time.Minute)
					if err != nil || recoveryLease.Source.ID != current.ID {
						t.Fatal("recovery lease", err)
					}
					if _, err = store.ApplyEnvironmentGitOps(ctx, recoveryLease, claimedIntentPlan(t, store, recoveryLease, recovery)); err != nil {
						t.Fatal("recovery intent", err)
					}
					recoveryPlan := claimedIntentPlan(t, store, recoveryLease, recovery)
					requests, err = prep.EnvironmentGitOpsSourceRequests(ctx, recoveryLease, recoveryPlan)
					if err != nil || len(requests) != 0 {
						t.Fatal("recovery requested archive", err)
					}
					candidates, err = prep.PrepareEnvironmentGitOpsCandidates(ctx, recoveryLease, recoveryPlan, nil)
					if err != nil || len(candidates) != 1 || candidates[0].DeploymentID == dep.ID {
						t.Fatal("recovery failed to create its own held candidate", err)
					}
					recoveryIntent, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(ctx, app.AccountID, app.ID, current.EnvironmentID)
					if err != nil || recoveryIntent.JobID != targetIntent.JobID || recoveryIntent.SourceRevision != "" {
						t.Fatal("recovery replaced durable Job identity", err)
					}
					activateAndServeArtifactJob(t, basic, recoveryLease, recoveryPlan, recoveryIntent, candidates[0].DeploymentID, root.Input.StorageKey)
					after, err = basic.ListBuildsForAccount(ctx, app.AccountID)
					if err != nil || len(after) != len(before) {
						t.Fatal("rollback enqueued build", err)
					}
					// Reusing code never keeps current trust after revocation.
					if err = basic.DeleteAppTrustedSigner(ctx, app.AccountID, app.ID, "company"); err != nil {
						t.Fatal(err)
					}
					if _, err = basic.(state.DeploymentRuntimeProducerInputStore).GetFreshDeploymentRuntimeProducerInputs(ctx, app.AccountID, app.ID, dep.ID); err == nil {
						t.Fatal("revoked publisher retained authority")
					}
					if _, claimed, err := basic.(state.LayerArtifactRetentionStore).ClaimLayerArtifactDeletion(ctx, root.Input.StorageKey); err != nil || claimed {
						t.Fatal("scheduled borrowed bytes collectible", err)
					}
				})
			})
		}
	}
}

func mustJSONArtifactJob(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func checkArtifactJobPredicateAndWriterFence(t *testing.T, pool *pgxpool.Pool, root, deployment string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var read, write bool
	if err := pool.QueryRow(ctx, `SELECT environment_build_artifact_matches(d,true),environment_build_artifact_valid(d,true) FROM deployments d WHERE id=$1`, deployment).Scan(&read, &write); err != nil || !read || !write {
		t.Fatal("read/write artifact predicates disagree", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(ctx, `SELECT 1 FROM source_build_rootfs WHERE id=$1 FOR UPDATE`, root); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT environment_build_artifact_matches(d,true) FROM deployments d WHERE id=$1`, deployment).Scan(&read); err != nil || !read {
		t.Fatal("read-only artifact predicate acquired a writer lock", err)
	}
	err = pool.QueryRow(ctx, `SELECT environment_build_artifact_valid(d,true) FROM deployments d WHERE id=$1`, deployment).Scan(&write)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatal("artifact binding bypassed provenance row lock", err)
	}
}

func pollArtifactJobSources(t *testing.T, basic gitOpsTestStore, definitions map[string]environmentsync.DesiredState, commits map[string]string) {
	t.Helper()
	poller := basic.(state.EnvironmentGitSourcePollStore)
	for range len(definitions) {
		now := time.Now().UTC()
		claim, err := poller.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		desired, ok := definitions[claim.Source.ID]
		if !ok {
			t.Fatal("unexpected source poll")
		}
		if err = poller.FinishEnvironmentGitSourcePoll(t.Context(), claim, state.EnvironmentGitSourcePollResult{CommitSHA: commits[claim.Source.ID], Digest: desired.Digest}, now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}

func activateAndServeArtifactJob(t *testing.T, basic gitOpsTestStore, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan, intent state.EnvironmentWorkloadIntent, deploymentID, key string) state.ProjectReleaseSet {
	t.Helper()
	ctx := t.Context()
	graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(ctx, lease, plan)
	if err != nil || graph.Phase != "prepared" {
		t.Fatal("prepare artifact graph", err)
	}
	activation := basic.(state.EnvironmentGitOpsGraphActivationStore)
	if _, active, err := activation.ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan); err != nil || active {
		t.Fatal("artifact bypassed fresh qualification", err)
	}
	requests, err := basic.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(ctx, lease, plan)
	if err != nil || len(requests) != 1 {
		t.Fatal("fresh qualification request", err)
	}
	qualifyScheduledSourceJob(t, basic, requests[0])
	release, active, err := activation.ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan)
	if err != nil || !active {
		t.Fatal("qualified artifact activation", err)
	}
	job, err := basic.JobGetByID(ctx, intent.JobID)
	if err != nil || job.Status != "active" || job.ImageResolvedDigest != "gregale-deployment:"+deploymentID || job.ImageStorageKey != key {
		t.Fatal("activated job artifact", err)
	}
	dep, err := basic.DeploymentByID(ctx, deploymentID)
	if err != nil || !state.EnvironmentGitOpsSourceJobArtifactMatches(intent, job, dep) {
		t.Fatal("active artifact mismatch", err)
	}
	serving := basic.(state.EnvironmentGitOpsWorkloadServingStore)
	pending, ready, err := serving.PrepareEnvironmentGitOpsWorkloadServing(ctx, lease, plan, nil)
	if err != nil || ready || len(pending.ScheduledJobAcks) != 0 {
		t.Fatal("activation reused old occurrence", err)
	}
	now := time.Now().UTC()
	run, created, err := basic.(state.JobScheduleOccurrenceStore).JobRunCreateScheduledOccurrence(ctx, job.ID, job.CronSchedule, job.CronTimezone, job.LastScheduledAt, now, state.JobScheduledOccurrenceOptions{ScheduledFor: now, ScheduleRevision: job.ScheduleRevision})
	if err != nil || !created || run.ImageResolvedDigestSnapshot != job.ImageResolvedDigest || run.ImageStorageKeySnapshot != key {
		t.Fatal("fresh occurrence", err)
	}
	placement := qualificationPlacement(t, basic, 4096)
	if _, err = basic.CreateAndClaimJobInstance(ctx, uuid.NewString(), job.ID, run.ID, 0, string(state.StateColdBooting), job.RAMMB, placement.NodeID, placement.WakeID, uuid.NewString(), time.Now().Add(time.Minute), placement.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = basic.JobRunRecompute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = basic.JobTaskMarkTerminal(ctx, run.ID, 0, "succeeded", 0, "succeeded", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = basic.JobRunRecompute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	served, ready, err := serving.PrepareEnvironmentGitOpsWorkloadServing(ctx, lease, plan, nil)
	if err != nil || !ready || len(served.ScheduledJobAcks) != 1 || served.ScheduledJobAcks[0].JobRunID != run.ID {
		t.Fatal("new occurrence did not prove serving", err)
	}
	evidence, err := basic.(state.EnvironmentGitOpsCurrentWorkloadEvidenceStore).CurrentEnvironmentGitOpsWorkloadEvidence(ctx, lease.Source.AccountID, lease.Source.ID)
	if err != nil || evidence == nil || !evidence.Serving || !evidence.Qualified || !evidence.Activated {
		t.Fatalf("current Job serving evidence disagrees with completed occurrence: %+v %v", evidence, err)
	}
	// An image materializer still cannot claim the internal artifact reference.
	if _, err = basic.(state.JobImageMaterializationClaimer).JobClaimImageMaterialization(ctx, job.ID, "oci-worker", time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("artifact Job entered OCI materialization", err)
	}
	return release
}
