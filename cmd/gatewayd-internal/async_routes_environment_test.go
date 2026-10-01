// adr: 375
package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAsyncRouteEnvironmentPinsSettingsAndSeparatesReceipts(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "stage-edge-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "stage-edge"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "edge-api", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 4, RetryPolicyJSON: json.RawMessage(`{"max_attempts":2}`),
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RetryPolicyJSON = json.RawMessage(`{"max_attempts":5}`)
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	var stageDeployment state.Deployment
	var stageRelease state.ProjectReleaseSet
	for _, scope := range []string{"production", "staging"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, scope, 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if scope == "staging" {
			stageDeployment, stageRelease = dep, release
		}
	}
	enqueuer := &asyncRouteEnqueuer{store: store}
	request := gateway.AsyncRouteRequest{AppID: app.ID, AccountID: account.ID, Method: "POST", Path: "/work", Payload: json.RawMessage(`{}`), IdempotencyKey: "same-key"}
	production, err := enqueuer.EnqueueAsyncRoute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if production.ID != uuid.NewSHA1(asyncRouteInvocationNamespace, []byte(app.ID+"\x00"+request.IdempotencyKey)).String() {
		t.Fatal("legacy production receipt changed")
	}
	request.Scope = "staging"
	stage, err := enqueuer.EnqueueAsyncRoute(ctx, request)
	if err != nil || stage.ID == production.ID || stage.ReleaseID != stageRelease.ID || stage.DeploymentID != stageDeployment.ID {
		t.Fatalf("stage receipt = %+v, %v", stage, err)
	}
	row, err := store.InvocationByID(ctx, stage.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(row.RetryPolicyJSON) != `{"max_attempts":5}` {
		t.Fatalf("stage used production retry settings: %s", row.RetryPolicyJSON)
	}
	settings.RetryPolicyJSON = json.RawMessage(`{"max_attempts":11}`)
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 1, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: stageDeployment.ID}}); err != nil {
		t.Fatal(err)
	}
	replayed, err := enqueuer.EnqueueAsyncRoute(ctx, request)
	if err != nil || replayed != stage {
		t.Fatalf("retained stage receipt changed = %+v, %v", replayed, err)
	}
	request.IdempotencyKey = "new-key"
	fresh, err := enqueuer.EnqueueAsyncRoute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	row, err = store.InvocationByID(ctx, fresh.ID)
	if err != nil || string(row.RetryPolicyJSON) != `{"max_attempts":5}` {
		t.Fatalf("desired-head edit changed tested settings: %s, %v", row.RetryPolicyJSON, err)
	}
	request.Headers = map[string]string{api.ReleaseHeader: production.ReleaseID}
	if _, err := enqueuer.EnqueueAsyncRoute(ctx, request); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("production release entered stage host: %v", err)
	}
	request.Headers = map[string]string{api.ReleaseHeader: stageRelease.ID}
	request.Scope = "production"
	if _, err := enqueuer.EnqueueAsyncRoute(ctx, request); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stage release entered production host: %v", err)
	}
	request.Scope, request.Headers, request.IdempotencyKey = "staging", nil, "conflicting-receipt"
	poisonedHeaders, _ := json.Marshal(map[string]string{api.ReleaseHeader: production.ReleaseID})
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: asyncRouteInvocationID(app.ID, "staging", request.IdempotencyKey),
		AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke, Headers: poisonedHeaders}); err != nil {
		t.Fatal(err)
	}
	if _, err := enqueuer.EnqueueAsyncRoute(ctx, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stage adopted conflicting production receipt: %v", err)
	}
	legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	request.Scope, request.IdempotencyKey = "default", "legacy-alias-receipt"
	request.Headers = map[string]string{api.RevisionHeader: legacy.ID}
	legacyReceipt, err := enqueuer.EnqueueAsyncRoute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Scope, request.Headers = "production", nil
	aliasReceipt, err := enqueuer.EnqueueAsyncRoute(ctx, request)
	if err != nil || aliasReceipt != legacyReceipt {
		t.Fatalf("legacy production receipt lost across scope alias: %+v, %v", aliasReceipt, err)
	}
}
