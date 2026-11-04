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
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsResolvedAppIdentityIsUnique(t *testing.T) {
	for _, keepOriginal := range []bool{true, false} {
		name := "retained-mapping"
		if keepOriginal {
			name = "implicit-mapping"
		}
		t.Run(name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				ctx := t.Context()
				store := basic.(intentTestStore)
				source, desired, app := intentFixture(t, store, "enforce")
				adoptWorkloadIntent(t, store.(intentTestStore), source)
				w := desired.Definition.Workloads["api"]
				w.App = ""
				desired.Definition.Workloads["api"] = w
				valid, err := environmentsync.Compile(desired.Definition)
				if err != nil {
					t.Fatal(err)
				}
				preview, err := basic.(state.EnvironmentGitOpsPreviewStore).PreviewEnvironmentGitOpsCommit(ctx, source, strings.Repeat("b", 40), valid)
				if err != nil || !preview.Plan.CanApply() {
					t.Fatalf("existing implicit identity was rejected: %+v %v", preview, err)
				}
				if !keepOriginal {
					delete(desired.Definition.Workloads, "api")
					desired.Definition.WorkloadPruningPolicy = "retain"
				}
				desired.Definition.Workloads["alias"] = api.EnvironmentWorkload{App: app.Slug, Variables: map[string]string{"MODE": "alias"}}
				desired, err = environmentsync.Compile(desired.Definition)
				if err != nil {
					t.Fatal(err)
				}
				preview, err = basic.(state.EnvironmentGitOpsPreviewStore).PreviewEnvironmentGitOpsCommit(ctx, source, strings.Repeat("b", 40), desired)
				if err != nil || preview.Plan.CanApply() {
					t.Fatalf("duplicate candidate mapping was accepted: %+v %v", preview, err)
				}
				source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("b", 40)))
				if err != nil {
					t.Fatal(err)
				}
				adoption, err := store.PreviewEnvironmentGitOpsAdoption(ctx, source.AccountID, source.ID)
				if err != nil || adoption.CanApply() || !strings.Contains(strings.Join(adoption.BlockingReasons, " "), "app is already mapped to workload/api") {
					t.Fatalf("adoption missed physical identity conflict: %+v %v", adoption, err)
				}
				if err := store.AdoptEnvironmentGitOps(ctx, source.AccountID, source.ID, adoption.Hash); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("blocked adoption mutated ownership: %v", err)
				}
				assertVariable(t, store, source, app, "production", "MODE", "console")
			})
		})
	}
}

func TestGitOpsCompanyWorkloadUsesProjectOwner(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		ctx := t.Context()
		identity, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "gitops-company@example.test", Plan: api.PlanPro})
		if err != nil {
			t.Fatal(err)
		}
		company, err := store.CreateOrg(ctx, state.Org{Slug: "gitops-company", Name: "GitOps Company", Plan: api.PlanPro})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddOrgMember(ctx, company.ID, identity.Account.ID, state.OrgRoleOwner, nil); err != nil {
			t.Fatal(err)
		}
		project, err := store.CreateProject(ctx, state.Project{AccountID: identity.Account.ID, OrgID: company.ID, Slug: "company-shop", RepoFullName: "example/company-shop", ProductionBranch: "main", InstallID: 42})
		if err != nil {
			t.Fatal(err)
		}
		source, err := store.CreateEnvironmentGitSource(ctx, project.AccountID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: project.RepoFullName, Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
			"api": {Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("a", 64)}, Runtime: json.RawMessage(`{"port":8080,"healthz":"/health"}`)},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("a", 40))); err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(ctx, uuid.NewString(), time.Now(), 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := planForStore(t, store.(state.EnvironmentGitOpsIntentStore), lease, desired)
		if _, err := store.(state.EnvironmentGitOpsWorkloadCreationStore).PrepareEnvironmentGitOpsWorkloads(ctx, lease, plan); err != nil {
			t.Fatal(err)
		}
		apps, err := store.ListApps(ctx, project.AccountID)
		if err != nil || len(apps) != 1 || apps[0].OrgID != company.ID || apps[0].ProjectID != project.ID {
			t.Fatalf("workload did not inherit the company project owner: %+v %v", apps, err)
		}
	})
}

func TestGitOpsBusyScheduledJobDrainsBeforeUpdate(t *testing.T) {
	for _, kind := range []string{"source", "image-command"} {
		t.Run(kind, func(t *testing.T) {
			stores(t, func(t *testing.T, store gitOpsTestStore) {
				ctx := t.Context()
				oldLease, desired, caller, instanceID, runID := scheduledServiceCallerFixture(t, store, kind)
				intent, err := store.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(ctx, oldLease.Source.AccountID, caller.AppID, oldLease.Source.EnvironmentID)
				if err != nil {
					t.Fatal(err)
				}
				before, err := store.JobGetByID(ctx, intent.JobID)
				if err != nil {
					t.Fatal(err)
				}
				queuedAt := before.LastScheduledAt.Add(time.Minute)
				queued, created, err := store.(state.JobScheduleOccurrenceStore).JobRunCreateScheduledOccurrence(ctx, before.ID, before.CronSchedule, before.CronTimezone, before.LastScheduledAt, queuedAt,
					state.JobScheduledOccurrenceOptions{ScheduledFor: queuedAt, ScheduleRevision: before.ScheduleRevision})
				if err != nil || !created {
					t.Fatalf("queued occurrence: %v %v", created, err)
				}
				before, err = store.JobGetByID(ctx, intent.JobID)
				if err != nil {
					t.Fatal(err)
				}
				control := store.(state.EnvironmentGitOpsControlStore)
				reported, err := control.UpdateEnvironmentGitSource(ctx, oldLease.Source.AccountID, oldLease.Source.ID,
					state.EnvironmentGitSourceUpdate{ExpectedGeneration: oldLease.Source.Generation, Mode: "report"})
				if err != nil {
					t.Fatal(err)
				}
				reportLease, err := store.ClaimEnvironmentGitOps(ctx, uuid.NewString(), time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.(state.EnvironmentGitOpsJobUpdateStore).PrepareEnvironmentGitOpsJobUpdates(ctx, reportLease,
					claimedIntentPlan(t, store.(intentTestStore), reportLease, desired)); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("report mode paused an active schedule: %v", err)
				}
				if _, err := control.UpdateEnvironmentGitSource(ctx, reported.AccountID, reported.ID,
					state.EnvironmentGitSourceUpdate{ExpectedGeneration: reported.Generation, Mode: "enforce"}); err != nil {
					t.Fatal(err)
				}
				w := desired.Definition.Workloads["report"]
				w.Variables = map[string]string{"MODE": "updated"}
				desired.Definition.Workloads["report"] = w
				desired, err = environmentsync.Compile(desired.Definition)
				if err != nil {
					t.Fatal(err)
				}
				source, err := store.EnvironmentGitSource(ctx, oldLease.Source.AccountID, oldLease.Source.ProjectID, "production")
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("c", 40))); err != nil {
					t.Fatal(err)
				}
				lease, err := store.ClaimEnvironmentGitOps(ctx, uuid.NewString(), time.Now(), 5*time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, store.(intentTestStore), lease, desired)
				for _, invalid := range []struct {
					lease state.EnvironmentGitOpsLease
					plan  environmentsync.Plan
				}{{oldLease, plan}, {lease, environmentsync.Plan{Hash: strings.Repeat("0", 64)}}} {
					if _, err := store.(state.EnvironmentGitOpsJobUpdateStore).PrepareEnvironmentGitOpsJobUpdates(ctx, invalid.lease, invalid.plan); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("unreviewed or stale pause accepted: %v", err)
					}
				}
				unchanged, err := store.JobGetByID(ctx, intent.JobID)
				if err != nil || !reflect.DeepEqual(before, unchanged) {
					t.Fatalf("rejected preparation changed Job: %+v %v", unchanged, err)
				}
				backend := environmentgitops.IntentBackend{Store: store.(state.EnvironmentGitOpsIntentStore)}
				for range 2 {
					steps, err := backend.Apply(ctx, lease, claimedIntentPlan(t, store.(intentTestStore), lease, desired))
					if err != nil || len(steps) != 1 || steps[0].Action != "drain" || steps[0].Status != "waiting" {
						t.Fatalf("busy Job did not wait for drain: %+v %v", steps, err)
					}
				}
				paused, err := store.JobGetByID(ctx, intent.JobID)
				if err != nil || paused.Status != "paused" || paused.ImageRef != before.ImageRef || string(paused.EnvOverrides) != string(before.EnvOverrides) {
					t.Fatalf("drain changed accepted Job inputs: %+v %v", paused, err)
				}
				current, err := store.(state.EnvironmentWorkloadIntentStore).EnvironmentWorkloadIntent(ctx, source.AccountID, caller.AppID, source.EnvironmentID)
				if err != nil || !reflect.DeepEqual(current, intent) {
					t.Fatalf("drain published new intent early: %+v %v", current, err)
				}
				if pg, ok := store.(*state.PgStore); ok {
					instances, err := pg.LiveInstancesByHostIP(ctx, "", "10.100.0.65")
					if err != nil || len(instances) != 1 || instances[0].AppID != caller.AppID || instances[0].DeploymentID != caller.DeploymentID {
						t.Fatalf("paused Job lost its accepted caller identity: %+v %v", instances, err)
					}
				}
				firedAt := time.Now().UTC().Add(time.Minute)
				if _, created, err := store.(state.JobScheduleOccurrenceStore).JobRunCreateScheduledOccurrence(ctx, paused.ID, paused.CronSchedule, paused.CronTimezone, paused.LastScheduledAt, firedAt,
					state.JobScheduledOccurrenceOptions{ScheduledFor: firedAt, ScheduleRevision: paused.ScheduleRevision}); err != nil || created {
					t.Fatalf("paused schedule admitted more work: %v %v", created, err)
				}
				if err := store.JobTaskMarkTerminal(ctx, runID, 0, "succeeded", 0, "succeeded", "", time.Now()); err != nil {
					t.Fatal(err)
				}
				if _, err := store.JobRunRecompute(ctx, runID); err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateInstanceState(ctx, instanceID, string(state.StateStopped)); err != nil {
					t.Fatal(err)
				}
				if steps, err := backend.Apply(ctx, lease, claimedIntentPlan(t, store.(intentTestStore), lease, desired)); err != nil || len(steps) != 1 || steps[0].Action != "drain" {
					t.Fatalf("queued work was ignored during drain: %+v %v", steps, err)
				}
				instance, err := store.InstanceByID(ctx, instanceID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateAndClaimJobInstance(ctx, uuid.NewString(), before.ID, queued.ID, 0, string(state.StateColdBooting), before.RAMMB,
					instance.NodeID, uuid.NewString(), uuid.NewString(), time.Now().Add(time.Minute), instance.NodeID); err != nil {
					t.Fatal("accepted queued work could not run while paused:", err)
				}
				if err := store.JobTaskMarkTerminal(ctx, queued.ID, 0, "succeeded", 0, "succeeded", "", time.Now()); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.Apply(ctx, lease, claimedIntentPlan(t, store.(intentTestStore), lease, desired)); err != nil {
					t.Fatal("drained update remained blocked:", err)
				}
				updated, err := store.JobGetByID(ctx, intent.JobID)
				if err != nil || updated.Status != "paused" || !strings.Contains(string(updated.EnvOverrides), "updated") {
					t.Fatalf("updated Job escaped qualification or kept old inputs: %+v %v", updated, err)
				}
				run, err := store.JobRunGetByID(ctx, queued.ID)
				if err != nil || string(run.EffectiveEnvSnapshot) != string(queued.EffectiveEnvSnapshot) || run.ImageStorageKeySnapshot != queued.ImageStorageKeySnapshot {
					t.Fatalf("update changed accepted execution inputs: %+v %v", run, err)
				}
			})
		})
	}
}
