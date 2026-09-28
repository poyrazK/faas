package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceRefManifestAppliesWorkPolicyBeforeEventBinding(t *testing.T) {
	srv, store, acct, app := sourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	manifest := &gregalemanifest.Manifest{
		WorkPolicies:  []gregalemanifest.WorkPolicy{{Name: "document-index", MaxRunningPerKey: 1, PendingUpdates: "keep_latest", DebounceMS: 3000}},
		EventTriggers: []gregalemanifest.EventTrigger{{Source: "documents", Type: "document.edited", WorkPolicy: "document-index", WorkKey: "data.document_id"}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	staged, problem := srv.applySourceRefManifest(ctx, acct, app, manifest, "", true)
	if problem != nil {
		t.Fatalf("apply manifest: %s", problem.Detail)
	}
	policies, err := store.ListAppWorkPolicies(ctx, app.ID)
	if err != nil || len(policies) != 1 {
		t.Fatalf("policies = %+v, %v", policies, err)
	}
	subs, err := store.ListEventSubscriptionsForApp(ctx, app.ID)
	if err != nil || len(subs) != 1 {
		t.Fatalf("subscriptions = %+v, %v", subs, err)
	}
	bindings, err := store.EventWorkBindingsByIDs(ctx, []string{subs[0].ID})
	if err != nil || bindings[subs[0].ID].PolicyName != "document-index" {
		t.Fatalf("bindings = %+v, %v", bindings, err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/apps/reports/event-subscriptions", nil)
	request.SetPathValue("slug", app.Slug)
	recorder := httptest.NewRecorder()
	srv.listEventSubscriptions(recorder, request, acct)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list subscriptions = %d %s", recorder.Code, recorder.Body.String())
	}
	var inventory api.EventSubscriptionListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Subscriptions) != 1 || inventory.Subscriptions[0].WorkPolicy != "document-index" || inventory.Subscriptions[0].WorkKey != "data.document_id" {
		t.Fatalf("subscription inventory = %+v", inventory)
	}
	second, problem := srv.applySourceRefManifest(ctx, acct, app, manifest, "", true)
	if problem != nil || sourceRefManifestNeedsRollback(second) {
		t.Fatalf("idempotent apply = %+v, %+v", second, problem)
	}
	if err := srv.rollbackSourceRefManifest(ctx, staged); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := store.AppWorkPolicyByName(ctx, app.ID, "document-index"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("policy after rollback = %v", err)
	}
	subs, err = store.ListEventSubscriptionsForApp(ctx, app.ID)
	if err != nil || len(subs) != 0 {
		t.Fatalf("subscriptions after rollback = %+v, %v", subs, err)
	}
}
