package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func newWorkloadFixture(t *testing.T, basic gitOpsTestStore, mode string, plan api.Plan) (state.EnvironmentGitSource, environmentsync.DesiredState, state.EnvironmentGitOpsLease) {
	t.Helper()
	jobQueueEnabled := false
	account, err := basic.CreateAccount(t.Context(), "new-workload@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	project, err := basic.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "new-workload", RepoFullName: "example/new", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	source, err := basic.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/new", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: mode, ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
		"api":      {ServiceBindings: map[string]api.EnvironmentServiceBinding{"function": {Workload: "function", EnvKey: "FUNCTION_URL"}}, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "example/api@sha256:" + strings.Repeat("a", 64)}, Runtime: json.RawMessage(`{"port":8081}`)},
		"function": {Source: &api.EnvironmentWorkloadSource{Kind: "function", Runtime: "node22"}, Runtime: json.RawMessage(`{"port":8080}`)},
		"job": {Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "example/job@sha256:" + strings.Repeat("b", 64)}, Runtime: json.RawMessage(`{"execution_mode":"job"}`),
			JobSmoke:      &api.EnvironmentJobSmoke{Command: []string{"node", "scripts/qualify.js"}, TimeoutSeconds: 30},
			QueueBindings: map[string]api.EnvironmentQueueBinding{"tasks": {QueueName: "tasks", Mode: "push", WorkloadClass: string(state.WorkloadClassJob), Enabled: &jobQueueEnabled, MaxConcurrency: 1}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := basic.ClaimEnvironmentGitOps(t.Context(), "new-workload", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return source, desired, lease
}

func TestEnvironmentGitOpsNewWorkloadReservation(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		source, desired, lease := newWorkloadFixture(t, basic, "enforce", api.PlanPro)
		store := basic.(state.EnvironmentGitOpsWorkloadCreationStore)
		intent := basic.(state.EnvironmentGitOpsIntentStore)
		plan := planForStore(t, intent, lease, desired)
		if !plan.CanApply() {
			t.Fatalf("new workload plan blocked: %+v", plan)
		}
		if _, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, environmentsync.Plan{Hash: strings.Repeat("0", 64)}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unreviewed reservation: %v", err)
		}
		steps, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan)
		if err != nil || len(steps) == 0 {
			t.Fatalf("reserve: %+v %v", steps, err)
		}
		if _, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old plan reused after identity creation: %v", err)
		}
		fresh := planForStore(t, intent, lease, desired)
		if !fresh.CanApply() || !fresh.HasDrift() || !containsEnvironmentQueueChange(fresh) {
			t.Fatalf("workload reservation should leave only its reviewed queue binding for the next apply: %+v", fresh)
		}
		if _, err := intent.ApplyEnvironmentGitOps(t.Context(), lease, fresh); err != nil {
			t.Fatalf("apply reviewed disabled job queue binding: %v", err)
		}
		fresh = planForStore(t, intent, lease, desired)
		if !fresh.CanApply() || fresh.HasDrift() {
			t.Fatalf("scoped workload and queue intent did not converge: %+v", fresh)
		}
		if steps, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, fresh); err != nil || len(steps) != 0 {
			t.Fatalf("retry created more resources: %+v %v", steps, err)
		}
		apps, err := basic.ListApps(t.Context(), source.AccountID)
		if err != nil || len(apps) != 3 {
			t.Fatalf("apps: %+v %v", apps, err)
		}
		ids := map[string]string{}
		for _, app := range apps {
			if app.Visibility != api.AppVisibilityInternal {
				t.Fatalf("reservation became public: %+v", app)
			}
			if deps, err := basic.ListDeploymentsForApp(t.Context(), app.ID, 10, 0); err != nil || len(deps) != 0 {
				t.Fatalf("reservation launched deployments: %+v %v", deps, err)
			}
			row, err := basic.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(t.Context(), source.AccountID, app.ID, source.EnvironmentID)
			if err != nil || row.Source == nil {
				t.Fatalf("source missing: %+v %v", row, err)
			}
			ids[row.Source.Kind] = app.ID
			if row.Source.Kind == "function" && (app.Type != state.AppTypeFunction || app.Runtime != "node22" || row.SourceRevision != lease.Revision.CommitSHA) {
				t.Fatalf("function runner or revision lost: %+v %+v", app, row)
			}
		}
		before, err := intent.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil || before.State.ResourceIDs["workload/function"] != ids["function"] {
			t.Fatalf("logical mapping lost: %+v %v", before, err)
		}
		if _, err := store.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, fresh); err != nil {
			t.Fatal(err)
		}
		after, err := intent.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil || !reflect.DeepEqual(before.State.ResourceIDs, after.State.ResourceIDs) {
			t.Fatalf("retry rebound identity: %+v %v", after, err)
		}

		// Exercise the next preparation boundary too: a newly reserved
		// function must keep its runner and original workload mapping when
		// the reviewed source is frozen into a held build candidate.
		candidateStore := basic.(state.EnvironmentGitOpsPreparationStore)
		candidatePlan := planForStore(t, intent, lease, desired)
		requests, err := candidateStore.EnvironmentGitOpsSourceRequests(t.Context(), lease, candidatePlan)
		if err != nil || len(requests) != 1 || requests[0].Resource != "workload/function" || requests[0].Source.Kind != "function" {
			t.Fatalf("new function source request: %+v %v", requests, err)
		}
		candidates, err := candidateStore.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, candidatePlan,
			map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: sourceArtifact(t, requests[0])})
		if err != nil || len(candidates) != 3 {
			t.Fatalf("prepare new workload cohort: %+v %v", candidates, err)
		}
		for _, candidate := range candidates {
			dep, err := basic.(state.Store).DeploymentByID(t.Context(), candidate.DeploymentID)
			if err != nil || !dep.EnvironmentWorkloadHeld() || dep.Status == state.DeployLive || dep.TrafficPercent != 0 {
				t.Fatalf("new workload candidate escaped hold: %+v %v", dep, err)
			}
			frozen, err := dep.ScopedWorkloadRuntime()
			if err != nil {
				t.Fatalf("new workload frozen input: %v", err)
			}
			switch frozen.Resource {
			case "workload/api":
				binding, ok := frozen.ServiceBindings["function"]
				if !ok || binding.TargetAppID != ids["function"] || binding.EnvKey != "FUNCTION_URL" {
					t.Fatalf("new API candidate lost its pinned function binding: %+v", frozen.ServiceBindings)
				}
			case "workload/function":
				if frozen.AppType != state.AppTypeFunction || frozen.RuntimeBase != "node22" || frozen.Source == nil || frozen.Source.Kind != "function" || frozen.SourceArchive == nil || frozen.SourceArchive.CommitSHA != lease.Revision.CommitSHA {
					t.Fatalf("new function candidate lost runner or reviewed source: %+v", frozen)
				}
			case "workload/job":
				queue, ok := frozen.QueueBindings["tasks"]
				observedQueueID, parseErr := uuid.Parse(after.State.ResourceIDs["workload/job/queue_bindings/tasks"])
				if frozen.WorkloadClass != state.WorkloadClassJob || frozen.Baseline.ExecutionMode != api.ExecutionModeJob || frozen.Source == nil || frozen.Source.Image == "" ||
					frozen.JobSmoke == nil || !reflect.DeepEqual(*frozen.JobSmoke, *desired.Definition.Workloads["job"].JobSmoke) ||
					!ok || parseErr != nil || queue.BindingID != observedQueueID.String() || queue.Contract.Enabled == nil || *queue.Contract.Enabled ||
					!dep.EnvironmentWorkloadHeld() || dep.Status == state.DeployLive || dep.TrafficPercent != 0 {
					t.Fatalf("new job candidate lost its frozen execution contract or exact disabled queue binding: binding=%+v observedID=%q frozen=%+v deployment=%+v", queue, after.State.ResourceIDs["workload/job/queue_bindings/tasks"], frozen, dep)
				}
			default:
				t.Fatalf("unexpected new workload candidate: %+v", frozen)
			}
		}
	})
}

func containsEnvironmentQueueChange(plan environmentsync.Plan) bool {
	for _, change := range plan.Changes {
		if strings.HasPrefix(change.Path, "queue_bindings/") {
			return true
		}
	}
	return false
}

func TestEnvironmentGitOpsNewWorkloadReservationIsAtomicAndEnforceOnly(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		plan       api.Plan
	}{{"quota", "enforce", api.PlanFree}, {"report", "report", api.PlanPro}} {
		t.Run(tc.name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				source, desired, lease := newWorkloadFixture(t, basic, tc.mode, tc.plan)
				plan := planForStore(t, basic.(state.EnvironmentGitOpsIntentStore), lease, desired)
				_, err := basic.(state.EnvironmentGitOpsWorkloadCreationStore).PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan)
				if err == nil {
					t.Fatal("forbidden reservation succeeded")
				}
				apps, err := basic.ListApps(t.Context(), source.AccountID)
				if err != nil || len(apps) != 0 {
					t.Fatalf("failed cohort leaked apps: %+v %v", apps, err)
				}
				after := planForStore(t, basic.(state.EnvironmentGitOpsIntentStore), lease, desired)
				if after.Hash != plan.Hash {
					t.Fatalf("failed cohort changed observation: before %s after %s", plan.Hash, after.Hash)
				}
			})
		})
	}
}

func TestEnvironmentGitOpsScheduledJobBindingLifecycle(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		ctx := t.Context()
		account, err := basic.CreateAccount(ctx, "scheduled-workload@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		project, err := basic.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "scheduled-workload",
			RepoFullName: "example/scheduled-workload", ProductionBranch: "main", InstallID: 43})
		if err != nil {
			t.Fatal(err)
		}
		source, err := basic.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
			RepositoryID: 124, InstallationID: 43, Repository: "example/scheduled-workload", Ref: "refs/heads/main",
			ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual", Prune: true})
		if err != nil {
			t.Fatal(err)
		}
		image := "registry.example/reports@sha256:" + strings.Repeat("a", 64)
		definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production",
			Workloads: map[string]api.EnvironmentWorkload{"report": {
				Source:   &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
				Runtime:  json.RawMessage(`{"execution_mode":"job"}`),
				JobSmoke: &api.EnvironmentJobSmoke{Command: []string{"/bin/true"}, TimeoutSeconds: 30},
				Schedule: &api.EnvironmentJobSchedule{Cron: "15 * * * *", Timezone: "UTC"},
			}}}
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		lease, err := basic.ClaimEnvironmentGitOps(ctx, "scheduled-job-creator", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		intentStore := basic.(state.EnvironmentGitOpsIntentStore)
		plan := planForStore(t, intentStore, lease, desired)
		if !plan.CanApply() || !plan.HasDrift() {
			t.Fatalf("scheduled workload plan: %+v", plan)
		}
		steps, err := basic.(state.EnvironmentGitOpsWorkloadCreationStore).PrepareEnvironmentGitOpsWorkloads(ctx, lease, plan)
		if err != nil || len(steps) == 0 {
			t.Fatalf("reserve scheduled workload: %+v %v", steps, err)
		}
		apps, err := basic.ListApps(ctx, account.ID)
		if err != nil || len(apps) != 1 {
			t.Fatalf("reserved workload: %+v %v", apps, err)
		}
		workloadIntents := basic.(state.EnvironmentWorkloadIntentStore)
		intent, err := workloadIntents.EnvironmentWorkloadIntent(ctx, account.ID, apps[0].ID, source.EnvironmentID)
		if err != nil || intent.JobID == "" {
			t.Fatalf("scheduled workload has no durable Job binding: %+v %v", intent, err)
		}
		job, err := basic.JobGetByID(ctx, intent.JobID)
		if err != nil || job.Status != "paused" || job.Kind != "recurring" || job.ImageRef != image ||
			job.CronSchedule != "15 * * * *" || job.CronTimezone != "UTC" || job.ImageMaterializationStatus != "pending" ||
			job.ImageStorageKey != "" || job.ImageResolvedDigest != "" || job.RAMMB != api.JobRAMMB[api.PlanPro.PlanIndex()] ||
			job.TaskTimeoutS != api.JobTaskTimeoutSec[api.PlanPro.PlanIndex()] || job.MaxParallelism != 1 || job.RetryMax != 0 ||
			len(job.Command) != 0 || string(job.EnvOverrides) != "{}" {
			t.Fatalf("scheduled Job did not preserve the reviewed paused contract: %+v %v", job, err)
		}
		active := "active"
		if _, err := basic.JobUpdate(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, &active); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("Jobs API mutation error = %v, want GitOps-managed error", err)
		}
		if _, _, err := basic.JobSoftDelete(ctx, job.ID); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("Jobs API delete error = %v, want GitOps-managed error", err)
		}
		job, err = basic.JobGetByID(ctx, intent.JobID)
		if err != nil || job.Status != "paused" {
			t.Fatalf("rejected API mutation changed the bound Job: %+v %v", job, err)
		}

		// A reviewed removal retires the scheduler record and clears the link.
		definition.Workloads["report"] = api.EnvironmentWorkload{Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
			Runtime: json.RawMessage(`{"execution_mode":"job"}`), JobSmoke: definition.Workloads["report"].JobSmoke}
		withoutSchedule, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(source, withoutSchedule, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		lease, err = basic.ClaimEnvironmentGitOps(ctx, "scheduled-job-retirer", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		retirementPlan := planForStore(t, intentStore, lease, withoutSchedule)
		if !retirementPlan.CanApply() || !retirementPlan.HasDrift() {
			t.Fatalf("schedule retirement plan: %+v", retirementPlan)
		}
		if _, err := intentStore.ApplyEnvironmentGitOps(ctx, lease, retirementPlan); err != nil {
			t.Fatalf("apply reviewed schedule removal: %v", err)
		}
		intent, err = workloadIntents.EnvironmentWorkloadIntent(ctx, account.ID, apps[0].ID, source.EnvironmentID)
		if err != nil || intent.JobID != "" {
			t.Fatalf("retired schedule retained its Job link: %+v %v", intent, err)
		}
		if _, err := basic.JobGetByID(ctx, job.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("retired Job remains customer-visible: %v", err)
		}
	})
}

func planForStore(t *testing.T, store state.EnvironmentGitOpsIntentStore, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) environmentsync.Plan {
	t.Helper()
	observation, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observation.State, observation.Owners, environmentsync.PlanOptions{Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation, Prune: lease.Source.Spec.Prune, Now: time.Now(), Overrides: observation.Overrides})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
