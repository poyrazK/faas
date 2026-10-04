// adr: 566
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestEnvironmentWorkPolicyAPIIsolatesConfigAndCancellation(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	var err error
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest, Debounce: time.Second}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	call := func(method, suffix, body, revision string) *httptest.ResponseRecorder {
		t.Helper()
		request, response := projectRequest(method, "/v1/apps/"+app.Slug+"/work-policies"+suffix, app.Slug, []byte(body))
		request.SetPathValue("name", policy.Name)
		if method == http.MethodPost {
			request.Header.Set("Idempotency-Key", "shared-cancellation-key")
		}
		if revision != "" {
			request.Header.Set("If-Workload-Revision", revision)
		}
		switch method {
		case http.MethodPut:
			srv.upsertWorkPolicy(response, request, account)
		case http.MethodGet:
			srv.listWorkPolicies(response, request, account)
		case http.MethodDelete:
			srv.deleteWorkPolicy(response, request, account)
		case http.MethodPost:
			srv.cancelPendingWork(response, request, account)
		}
		return response
	}
	assertProblem(t, call(http.MethodGet, "?environment=staging", "", ""), http.StatusConflict, "environment_work_policy_collection_unavailable")
	body := `{"max_running_per_key":1,"pending_updates":"keep_latest","debounce_ms":3000}`
	put := call(http.MethodPut, "/orders?environment=staging", body, "0")
	if put.Code != http.StatusOK || put.Header().Get("X-Gregale-Workload-Revision") != "1" {
		t.Fatalf("stage put = %d %s", put.Code, put.Body.String())
	}
	var configured api.WorkPolicyResponse
	if err := json.Unmarshal(put.Body.Bytes(), &configured); err != nil || configured.DebounceMS != 3000 || configured.Revision != 1 {
		t.Fatalf("stage policy = %+v, %v", configured, err)
	}
	listed := call(http.MethodGet, "?environment=staging", "", "")
	if listed.Code != http.StatusOK || listed.Header().Get("X-Gregale-Workload-Config-Hash") != put.Header().Get("X-Gregale-Workload-Config-Hash") {
		t.Fatalf("stage list = %d %s", listed.Code, listed.Body.String())
	}
	assertProblem(t, call(http.MethodPut, "/orders?environment=staging", body, "0"), http.StatusConflict, api.CodeConflict)
	assertProblem(t, call(http.MethodPut, "/orders?environment=staging", body, "invalid"), http.StatusBadRequest, api.CodeValidation)
	noop := call(http.MethodPut, "/orders?environment=staging", body, "1")
	if noop.Code != http.StatusOK || noop.Header().Get("X-Gregale-Workload-Revision") != "1" {
		t.Fatalf("no-op advanced desired head: %d %s", noop.Code, noop.Body.String())
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}); err != nil {
		t.Fatal(err)
	}
	stageRequest, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store,
		state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke, Payload: json.RawMessage(`{}`)}, "staging")
	if err != nil {
		t.Fatal(err)
	}
	stagePolicy := policy
	stagePolicy.Debounce = 3 * time.Second
	stageRow, err := store.EnqueueKeyedInvocation(ctx, stageRequest, stagePolicy, "s:one")
	if err != nil {
		t.Fatal(err)
	}
	// A stage cancellation cannot touch an already admitted production lane.
	row, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
		Payload: json.RawMessage(`{}`), WorkPolicyRevision: 1}, policy, "s:one")
	if err != nil {
		t.Fatal(err)
	}
	stageCancel := call(http.MethodPost, "/orders/cancel-pending?environment=staging", `{"key":"one"}`, "")
	var stageReceipt api.CancelPendingWorkResponse
	if err := json.Unmarshal(stageCancel.Body.Bytes(), &stageReceipt); stageCancel.Code != http.StatusOK || err != nil || stageReceipt.CancelledCount != 1 {
		t.Fatalf("stage cancellation = %d %s, %v", stageCancel.Code, stageCancel.Body.String(), err)
	}
	if after, err := store.InvocationByID(ctx, stageRow.ID); err != nil || after.State != state.InvocationCancelled {
		t.Fatalf("stage cancellation missed own row: %+v, %v", after, err)
	}
	if after, err := store.InvocationByID(ctx, row.ID); err != nil || after.State != state.InvocationPending {
		t.Fatalf("stage cancellation changed production row: %+v, %v", after, err)
	}
	productionCancel := call(http.MethodPost, "/orders/cancel-pending", `{"key":"one"}`, "")
	if productionCancel.Code != http.StatusOK {
		t.Fatalf("stage guard poisoned production receipt: %d %s", productionCancel.Code, productionCancel.Body.String())
	}
	var productionReceipt api.CancelPendingWorkResponse
	if err := json.Unmarshal(productionCancel.Body.Bytes(), &productionReceipt); err != nil || productionReceipt.CancelledCount != 1 || productionReceipt.ID == stageReceipt.ID {
		t.Fatalf("production reused stage receipt: %+v, %v", productionReceipt, err)
	}
	newRow, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
		Payload: json.RawMessage(`{}`), WorkPolicyRevision: 1}, policy, "s:one")
	if err != nil {
		t.Fatal(err)
	}
	newStageRow, err := store.EnqueueKeyedInvocation(ctx, stageRequest, stagePolicy, "s:one")
	if err != nil {
		t.Fatal(err)
	}
	if replay := call(http.MethodPost, "/orders/cancel-pending?environment=staging", `{"key":"one"}`, ""); replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != stageCancel.Body.String() {
		t.Fatalf("stage receipt did not replay independently: %d %s", replay.Code, replay.Body.String())
	}
	if after, err := store.InvocationByID(ctx, newStageRow.ID); err != nil || after.State != state.InvocationPending {
		t.Fatalf("stage replay cancelled newer work: %+v, %v", after, err)
	}
	if replay := call(http.MethodPost, "/orders/cancel-pending", `{"key":"one"}`, ""); replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("legacy receipt stopped replaying: %d %s", replay.Code, replay.Body.String())
	}
	if after, err := store.InvocationByID(ctx, newRow.ID); err != nil || after.State != state.InvocationPending {
		t.Fatalf("stage request/replayed receipt cancelled newer production row: %+v, %v", after, err)
	}
	deleted := call(http.MethodDelete, "/orders?environment=staging", "", "1")
	if deleted.Code != http.StatusNoContent || deleted.Header().Get("X-Gregale-Workload-Revision") != "2" {
		t.Fatalf("stage delete = %d %s", deleted.Code, deleted.Body.String())
	}
	if list := call(http.MethodGet, "?environment=staging", "", ""); list.Code != http.StatusOK || list.Body.String() != "{\"policies\":[]}\n" {
		t.Fatalf("last deletion did not retain explicit empty: %d %s", list.Code, list.Body.String())
	}
	if original, err := store.AppWorkPolicyByName(ctx, app.ID, "orders"); err != nil || original.Policy.Debounce != time.Second || original.Revision != 1 {
		t.Fatalf("stage edits changed production config: %+v, %v", original, err)
	}
	recreated := call(http.MethodPut, "/orders?environment=staging", body, "2")
	if recreated.Code != http.StatusOK {
		t.Fatalf("recreate = %d %s", recreated.Code, recreated.Body.String())
	}
	if err := json.Unmarshal(recreated.Body.Bytes(), &configured); err != nil || configured.Revision != 3 {
		t.Fatalf("recreated policy revision = %+v, %v", configured, err)
	}
	for _, query := range []string{"?environment=", "?scope=staging", "?environment=staging&environment=production", "?environment=default", "?environment=bad!"} {
		assertProblem(t, call(http.MethodPut, "/orders"+query, body, ""), http.StatusBadRequest, api.CodeValidation)
		assertProblem(t, call(http.MethodDelete, "/orders"+query, "", ""), http.StatusBadRequest, api.CodeValidation)
		assertProblem(t, call(http.MethodPost, "/orders/cancel-pending"+query, `{"key":"one"}`, ""), http.StatusBadRequest, api.CodeValidation)
	}
	assertProblem(t, call(http.MethodPut, "/orders?environment=unknown", body, ""), http.StatusNotFound, api.CodeNotFound)
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, call(http.MethodPut, "/orders?environment=staging", body, "3"), http.StatusConflict, api.CodeConflict)
	assertProblem(t, call(http.MethodDelete, "/orders?environment=staging", "", "3"), http.StatusConflict, api.CodeConflict)
	if list := call(http.MethodGet, "", "", ""); list.Code != http.StatusOK {
		t.Fatalf("legacy production list = %d %s", list.Code, list.Body.String())
	}
}

func TestEnvironmentWorkCancellationAllowsIdleDeletion(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	env, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	request, response := projectRequest(http.MethodPost, "/v1/apps/"+app.Slug+"/work-policies/orders/cancel-pending?environment=staging", app.Slug, []byte(`{"key":"one"}`))
	request.SetPathValue("name", "orders")
	request.Header.Set("Idempotency-Key", "stage-lifecycle-receipt")
	srv.cancelPendingWork(response, request, account)
	if response.Code != http.StatusOK {
		t.Fatalf("empty stage cancellation = %d %s", response.Code, response.Body.String())
	}
	var original api.CancelPendingWorkResponse
	if err := json.Unmarshal(response.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	request, response = projectRequest(http.MethodDelete, "/v1/projects/"+project.Slug+"/environments/staging", project.Slug, nil)
	request.SetPathValue("environment", "staging")
	srv.deleteProjectEnvironment(response, request, account)
	if response.Code != http.StatusNoContent {
		t.Fatalf("idle stage deletion = %d %s", response.Code, response.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), account.ID, project.ID, env.Slug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted stage remains: %v", err)
	}
	recreated, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: env.Slug})
	if err != nil || recreated.ID == env.ID {
		t.Fatalf("recreated environment identity: %+v, %v", recreated, err)
	}
	request, response = projectRequest(http.MethodPost, "/v1/apps/"+app.Slug+"/work-policies/orders/cancel-pending?environment=staging", app.Slug, []byte(`{"key":"one"}`))
	request.SetPathValue("name", "orders")
	request.Header.Set("Idempotency-Key", "stage-lifecycle-receipt")
	srv.cancelPendingWork(response, request, account)
	var fresh api.CancelPendingWorkResponse
	if err := json.Unmarshal(response.Body.Bytes(), &fresh); err != nil || response.Code != http.StatusOK || fresh.ID == original.ID {
		t.Fatalf("recreated stage replayed old receipt: %d %s, %v", response.Code, response.Body.String(), err)
	}
}

func TestEnvironmentDeletionReportsRunningWork(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	ctx := t.Context()
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}); err != nil {
		t.Fatal(err)
	}
	prepared, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store,
		state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/work"}, "staging")
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.EnqueueInvocation(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, row.ID, "", 30, 10); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	remove := func() *httptest.ResponseRecorder {
		r, w := projectRequest(http.MethodDelete, "/v1/projects/"+project.Slug+"/environments/staging", project.Slug, nil)
		r.SetPathValue("environment", "staging")
		srv.deleteProjectEnvironment(w, r, account)
		return w
	}
	assertProblem(t, remove(), http.StatusConflict, "environment_work_busy")
	if err := store.CompleteInvocation(ctx, row.ID, nil); err != nil {
		t.Fatal(err)
	}
	if response := remove(); response.Code != http.StatusNoContent {
		t.Fatalf("finished stage deletion = %d %s", response.Code, response.Body.String())
	}
}
