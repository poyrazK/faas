//go:build !no_pg

// adr: 567
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestPgEnvironmentWorkPoliciesAreIsolatedAndPinned(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentWorkPoliciesAreIsolatedAndPinned(t, store)
}

func TestPgCloneStagePolicyCollectionUsesPinnedConfigAndPreservesEmpty(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	account, err := store.CreateAccount(ctx, "pinned-stage-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "stage-policy"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "stage-policy-app", WorkloadName: "api", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 1800}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "source-stage"}); err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, Debounce: time.Second}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	policy.Debounce = 2 * time.Second
	_, desired, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "source-stage", nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(key string) {
		t.Helper()
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "source-stage", Kind: state.DeploymentKindImage, ImageDigest: "sha256:policy"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/policy.ext4", "layers/"+key, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	deploy("stage-policy")
	policy.Debounce = 3 * time.Second
	if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "source-stage", nil, policy); err != nil {
		t.Fatal(err)
	}
	request := state.ProjectEnvironmentCloneCaptureRequest{AccountID: account.ID, ProjectID: project.ID, SourceEnvironment: "source-stage", TargetEnvironment: "target-stage", IdempotencyKey: "pinned-policy"}
	op, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, request)
	if err != nil {
		t.Fatalf("capture stage pinned policy: %v", err)
	}
	readBook := func(operationID string) *state.ProjectEnvironmentWorkPolicySettings {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, "select snapshot from project_environment_clone_workloads where operation_id=$1 and app_id=$2", operationID, app.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var captured struct {
			Settings state.ProjectEnvironmentWorkloadSettings `json:"settings"`
			Policies struct {
				Work state.ProjectEnvironmentCloneWorkPolicyDefinitions `json:"work"`
			} `json:"policies"`
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatal(err)
		}
		if captured.Settings.WorkPolicies == nil || !reflect.DeepEqual(captured.Settings.WorkPolicies.Policies, captured.Policies.Work.Policies) {
			t.Fatalf("capture book/catalogue differ: %+v", captured)
		}
		return captured.Settings.WorkPolicies
	}
	book := readBook(op.ID)
	if !reflect.DeepEqual(book, desired.Settings.WorkPolicies) || book.Policies[0].DebounceMS != 2000 {
		t.Fatalf("capture read live desired/production policy: %+v", book)
	}
	// A later production policy and an extra production name cannot populate
	// the source stage's already pinned complete collection.
	policy.Debounce = 4 * time.Second
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, workpolicy.Policy{Name: "production-only", MaxRunningPerKey: 1}); err != nil {
		t.Fatal(err)
	}
	if replay, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, request); err != nil || replay.ID != op.ID || replay.SourceRevisionHash != op.SourceRevisionHash {
		t.Fatalf("replay recaptured source: %+v, %v", replay, err)
	}
	newRequest := request
	newRequest.TargetEnvironment, newRequest.IdempotencyKey = "same-stage", "same-policies"
	if same, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, newRequest); err != nil || same.SourceRevisionHash != op.SourceRevisionHash {
		t.Fatalf("global edit changed pinned stage root: %+v, %v", same, err)
	}
	empty, err := state.DeleteEnvironmentWorkPolicy(ctx, store, app, "source-stage", policy.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	deploy("empty-stage-policy")
	newRequest.TargetEnvironment, newRequest.IdempotencyKey = "empty-stage", "empty-policies"
	emptyOp, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, newRequest)
	if err != nil || emptyOp.SourceRevisionHash == op.SourceRevisionHash {
		t.Fatalf("new deployment did not capture changed empty collection: %+v, %v", emptyOp, err)
	}
	if emptyBook := readBook(emptyOp.ID); !reflect.DeepEqual(emptyBook, empty.Settings.WorkPolicies) || emptyBook.Policies == nil || len(emptyBook.Policies) != 0 {
		t.Fatalf("empty capture inherited production: %+v", emptyBook)
	}
	// Application-wide producer bindings still lack independent ownership.
	// A binding without a scoped definition must fail by name, not disappear.
	sub, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.updated", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEventWorkBinding(ctx, app.ID, sub.ID, policy.Name, "data.order_id"); err != nil {
		t.Fatal(err)
	}
	newRequest.TargetEnvironment, newRequest.IdempotencyKey = "unsupported-stage", "unsupported-producer"
	if _, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, newRequest); !errors.Is(err, state.ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable) {
		t.Fatalf("unowned producer silently omitted or inherited: %v", err)
	}
	if _, err := store.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, account.ID, project.ID, newRequest.IdempotencyKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed producer capture left partial operation: %v", err)
	}
}
