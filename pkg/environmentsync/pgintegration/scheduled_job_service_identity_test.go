package pgintegration_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgGitOpsScheduledJobServiceIdentity(t *testing.T) {
	for _, kind := range []string{"image", "image-command", "source", "dockerfile"} {
		t.Run(kind, func(t *testing.T) {
			store := state.NewPgStore(pgtest.OpenMigrated(t))
			lease, desired, caller, instanceID, runID := scheduledServiceCallerFixture(t, store, kind)
			assertCaller := func(want bool) {
				t.Helper()
				instances, err := store.LiveInstancesByHostIP(t.Context(), "", "10.100.0.65")
				if err != nil || len(instances) != 1 {
					t.Fatalf("caller lookup: %+v %v", instances, err)
				}
				if !want {
					if instances[0].AppID != "" || instances[0].DeploymentID != "" {
						t.Fatalf("unclaimed Job retained caller identity: %+v", instances)
					}
					return
				}
				if instances[0].AppID != caller.AppID || instances[0].DeploymentID != caller.DeploymentID {
					t.Fatalf("scheduled Job lost its active release identity: %+v", instances)
				}
				route, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(t.Context(), caller.AppID, caller.DeploymentID, "api")
				if err != nil || !managed || !found || route.TargetAppID == "" {
					t.Fatalf("scheduled Job private route: %+v %v %v %v", route, managed, found, err)
				}
			}
			assertCaller(true)
			for _, control := range []state.EnvironmentGitSourceUpdate{
				{Suspended: new(true)}, {Suspended: new(false)}, {Mode: "report"}, {Mode: "enforce"},
			} {
				source, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
				if err != nil {
					t.Fatal(err)
				}
				control.ExpectedGeneration = source.Generation
				if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, control); err != nil {
					t.Fatal(err)
				}
				assertCaller(true)
			}
			// Approval alone does not replace the release that owns this running
			// occurrence, including its old source commit and frozen intent.
			source, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, "production")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("e", 40))); err != nil {
				t.Fatal(err)
			}
			assertCaller(true)
			if _, err := store.JobRunRecompute(t.Context(), runID); err != nil {
				t.Fatal(err)
			}
			if err := store.JobTaskMarkTerminal(t.Context(), runID, 0, "succeeded", 0, "succeeded", "", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			assertCaller(false)
			if err := store.UpdateInstanceState(t.Context(), instanceID, string(state.StateStopped)); err != nil {
				t.Fatal(err)
			}
			if instances, err := store.LiveInstancesByHostIP(t.Context(), "", "10.100.0.65"); err != nil || len(instances) != 0 {
				t.Fatalf("retired Job remained discoverable: %+v %v", instances, err)
			}
		})
	}
}

// This fixture uses the real preparation, qualification, activation and
// occurrence APIs. Its native receipts are modeled; it does not prove KVM.
func scheduledServiceCallerFixture(t *testing.T, store gitOpsTestStore, kind string) (state.EnvironmentGitOpsLease, environmentsync.DesiredState, state.EnvironmentWorkloadQualificationRequest, string, string) {
	t.Helper()
	ctx := t.Context()
	first, desired, initialIntent, _ := sourceScheduledJobFixture(t, store, "github", "source", true)
	app, err := store.CreateApp(ctx, state.App{AccountID: first.Source.AccountID, ProjectID: first.Source.ProjectID,
		Slug: "report-api", WorkloadName: "api", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP, Status: state.AppActive,
		RAMMB: 512, MaxConcurrency: 1, Manifest: state.AppManifest{Port: 8080, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	w := desired.Definition.Workloads["report"]
	w.ServiceBindings = map[string]api.EnvironmentServiceBinding{"api": {Workload: "api", EnvKey: "API_URL"}}
	switch kind {
	case "image", "image-command":
		w.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/report@sha256:" + strings.Repeat("d", 64)}
		if kind == "image" {
			w.Runtime = json.RawMessage(`{"execution_mode":"job"}`)
		}
	case "dockerfile":
		w.Source = &api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "jobs/report", Dockerfile: "Dockerfile"}
	}
	desired.Definition.Workloads["report"] = w
	desired.Definition.Workloads["api"] = api.EnvironmentWorkload{App: app.Slug,
		Source:  &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("c", 64)},
		Runtime: json.RawMessage(`{"port":8080,"healthz":"/readyz"}`)}
	desired, err = environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.EnvironmentGitSource(ctx, first.Source.AccountID, first.Source.ProjectID, "production")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("b", 40)))
	if err != nil {
		t.Fatal(err)
	}
	adoptWorkloadIntent(t, store.(intentTestStore), source)
	lease, err := store.ClaimEnvironmentGitOps(ctx, "scheduled-service", time.Now(), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.EnvironmentGitOpsIntentStore).ApplyEnvironmentGitOps(ctx, lease, claimedIntentPlan(t, store.(intentTestStore), lease, desired)); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(kind, "image") {
		job, err := store.JobGetByID(ctx, initialIntent.JobID)
		if err != nil {
			t.Fatal(err)
		}
		_, digest, _ := strings.Cut(job.ImageRef, "@")
		if _, err := store.(state.JobImageMaterializationStore).JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", digest, "image/report.ext4", ""); err != nil {
			t.Fatal(err)
		}
	}
	plan := claimedIntentPlan(t, store.(intentTestStore), lease, desired)
	requests, err := store.(state.EnvironmentGitOpsPreparationStore).EnvironmentGitOpsSourceRequests(ctx, lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
	for _, request := range requests {
		artifacts[request.Resource] = sourceArtifact(t, request)
	}
	candidates, err := store.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsCandidates(ctx, lease, plan, artifacts)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("service Job candidates: %+v %v", candidates, err)
	}
	for _, candidate := range candidates {
		if err := store.SetDeploymentRootfs(ctx, candidate.DeploymentID, "/reviewed.ext4", "candidate/"+candidate.DeploymentID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if graph, err := store.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(ctx, lease, plan); err != nil || graph.Phase != "prepared" {
		t.Fatalf("service Job graph: %+v %v", graph, err)
	}
	cohort, err := store.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(ctx, lease, plan)
	if err != nil || len(cohort) != 2 {
		t.Fatalf("service Job qualification: %d %v", len(cohort), err)
	}
	placement := qualificationPlacement(t, store, 4096)
	claimed, err := store.(state.EnvironmentGitOpsQualificationGraphDispatchStore).ClaimEnvironmentWorkloadQualificationGraphForNode(ctx, cohort[0].GraphID, placement.NodeID, "scheduled-service", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	completeCandidateBindingCohort(t, store.(candidateBindingTestStore), claimed, placement, true)
	release, active, err := store.(state.EnvironmentGitOpsGraphActivationStore).ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan)
	if err != nil || !active || len(release.Members) != 2 {
		evidence, evidenceErr := store.(state.EnvironmentGitOpsActivationEvidenceStore).EnvironmentGitOpsActivationEvidence(ctx, lease, plan)
		t.Fatalf("service Job activation: %+v %v; evidence=%+v (%v)", release, err, evidence, evidenceErr)
	}
	var caller state.EnvironmentWorkloadQualificationRequest
	for _, request := range claimed {
		if request.ExecutionMode == api.ExecutionModeJob {
			caller = request
		}
	}
	intent, err := store.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(ctx, source.AccountID, caller.AppID, source.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobGetByID(ctx, intent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run, created, err := store.(state.JobScheduleOccurrenceStore).JobRunCreateScheduledOccurrence(ctx, job.ID, job.CronSchedule, job.CronTimezone, job.LastScheduledAt, now,
		state.JobScheduledOccurrenceOptions{ScheduledFor: now, ScheduleRevision: job.ScheduleRevision})
	if err != nil || !created {
		t.Fatalf("service Job occurrence: %+v %v", run, err)
	}
	instanceID := uuid.NewString()
	if _, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 0, string(state.StateColdBooting), job.RAMMB,
		placement.NodeID, uuid.NewString(), uuid.NewString(), now.Add(time.Minute), placement.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishInstanceRuntime(ctx, instanceID, string(state.StateColdBooting), "scheduled-service", "10.100.0.65", 21000); err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobRunRecompute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	return lease, desired, caller, instanceID, run.ID
}
