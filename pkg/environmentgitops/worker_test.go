package environmentgitops_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type backend struct {
	observation environmentgitops.Observation
	apply       func(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]environmentgitops.Step, error)
	observed    int
	applied     int
}

func (b *backend) Observe(_ context.Context, _ state.EnvironmentGitOpsLease, _ environmentsync.DesiredState) (environmentgitops.Observation, error) {
	b.observed++
	return b.observation, nil
}

func (b *backend) Apply(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) ([]environmentgitops.Step, error) {
	b.applied++
	if b.apply != nil {
		return b.apply(ctx, lease, plan)
	}
	return nil, nil
}

func setup(t *testing.T, mode string) (*state.MemStore, state.EnvironmentGitSource, environmentsync.DesiredState, *backend, *environmentgitops.Worker) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "gitops-worker@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", RepoFullName: "example/shop", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: mode, ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {Variables: map[string]string{"MODE": "production"}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: source.AccountID, SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	b := &backend{observation: environmentgitops.Observation{State: environmentsync.ObservedState{Fields: append([]environmentsync.Field(nil), desired.Fields...)}}}
	for _, field := range desired.Fields {
		b.observation.Owners = append(b.observation.Owners, environmentsync.Ownership{Field: field, Manager: source.ID})
	}
	worker := &environmentgitops.Worker{Store: store, Backend: b, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
	return store, source, desired, b, worker
}

func lastRun(t *testing.T, store *state.MemStore, source state.EnvironmentGitSource) state.EnvironmentGitOpsRun {
	t.Helper()
	runs, err := store.ListEnvironmentGitOpsRuns(context.Background(), source.AccountID, source.ID, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("missing run result: %+v %v", runs, err)
	}
	return runs[0]
}

func TestWorkerReportsDriftWithoutApplying(t *testing.T) {
	store, source, _, b, worker := setup(t, "report")
	b.observation.State.Fields[1].Value = json.RawMessage(`"console-edit"`)
	worked, err := worker.RunOnce(context.Background())
	if err != nil || !worked || b.applied != 0 || lastRun(t, store, source).Status != "drifted" {
		t.Fatalf("report mode mutated intent or hid drift: worked=%v err=%v backend=%+v", worked, err, b)
	}
}

func TestWorkerReobservesBeforePublishingAppliedRevision(t *testing.T) {
	for _, effect := range []bool{false, true} {
		t.Run(map[bool]string{false: "executor-no-effect", true: "converged"}[effect], func(t *testing.T) {
			store, source, desired, b, worker := setup(t, "enforce")
			b.observation.State.Fields[1].Value = json.RawMessage(`"console-edit"`)
			b.apply = func(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]environmentgitops.Step, error) {
				if effect {
					b.observation.State.Fields = desired.Fields
				}
				return []environmentgitops.Step{{Resource: "workload/api", Path: "variables/MODE", Action: "update", Status: "completed"}}, nil
			}
			worked, err := worker.RunOnce(context.Background())
			if err != nil || !worked || b.applied != 1 || b.observed != 2 {
				t.Fatalf("executor result was trusted without verification: %v %v %+v", worked, err, b)
			}
			status, _ := store.EnvironmentGitSource(context.Background(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
			run := lastRun(t, store, source)
			if effect && (run.Status != "converged" || status.AppliedRevisionID != source.ApprovedRevisionID) || !effect && (run.Status != "drifted" || status.AppliedRevisionID != "") {
				t.Fatalf("false applied revision: %+v %+v", status, run)
			}
		})
	}
}

func TestWorkerPreservesPartialProgressAndDoesNotStoreRawErrors(t *testing.T) {
	store, source, _, b, worker := setup(t, "enforce")
	b.observation.State.Fields[1].Value = json.RawMessage(`"console-edit"`)
	b.apply = func(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]environmentgitops.Step, error) {
		return []environmentgitops.Step{{Resource: "workload/api", Path: "variables/MODE", Action: "update", Status: "completed"}}, errors.New("provider failed: must-not-leak")
	}
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	run := lastRun(t, store, source)
	encoded, _ := json.Marshal(run)
	if run.Status != "partial" || run.ErrorCode != "environment_apply_failed" || string(run.Steps) == "[]" || strings.Contains(string(encoded), "must-not-leak") {
		t.Fatalf("partial result missing or raw backend error exposed: %+v", run)
	}
}

func TestWorkerDoesNotExecuteOwnershipConflictsOrRestoreActiveOverrides(t *testing.T) {
	for _, action := range []string{"conflict", "override"} {
		t.Run(action, func(t *testing.T) {
			store, source, _, b, worker := setup(t, "enforce")
			if action == "conflict" {
				b.observation.Owners[1].Manager = "terraform"
			} else {
				field := b.observation.State.Fields[1]
				b.observation.Overrides = []environmentsync.Override{{Resource: field.Resource, Path: field.Path, ExpiresAt: time.Now().Add(time.Hour)}}
				b.observation.State.Fields[1].Value = json.RawMessage(`"temporary"`)
				// Adapters skip overridden changes, leaving them visible in the
				// final verification. No update may be proposed for this field.
				b.apply = func(_ context.Context, _ state.EnvironmentGitOpsLease, plan environmentsync.Plan) ([]environmentgitops.Step, error) {
					for _, change := range plan.Changes {
						if change.Path == field.Path && change.Action != "overridden" {
							t.Fatalf("override was scheduled for restoration: %+v", change)
						}
					}
					return nil, nil
				}
			}
			if _, err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			run := lastRun(t, store, source)
			if action == "conflict" && (run.Status != "blocked" || b.applied != 0) || action == "override" && run.Status != "overridden" {
				t.Fatalf("authority contract ignored: %+v %+v", run, b)
			}
		})
	}
}

func TestWorkerCannotFinishAfterNewRevisionIsApprovedDuringApply(t *testing.T) {
	store, source, desired, b, worker := setup(t, "enforce")
	b.observation.State.Fields[1].Value = json.RawMessage(`"console-edit"`)
	b.apply = func(ctx context.Context, _ state.EnvironmentGitOpsLease, _ environmentsync.Plan) ([]environmentgitops.Step, error) {
		_, _, err := store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: source.AccountID, SourceID: source.ID, ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("b", 40), Desired: desired, ApprovedBy: "owner"})
		b.observation.State.Fields = desired.Fields
		return nil, err
	}
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, _ := store.EnvironmentGitSource(context.Background(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if status.Generation != 2 || status.AppliedRevisionID != "" || lastRun(t, store, source).Status == "converged" {
		t.Fatalf("old generation published success: %+v", status)
	}
}
