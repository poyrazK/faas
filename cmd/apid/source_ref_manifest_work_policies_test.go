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

func TestSourceRefManifestAppliesWorkPolicyBeforeBrokerBinding(t *testing.T) {
	srv, store, acct, app := sourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	manifest := &gregalemanifest.Manifest{
		WorkPolicies: []gregalemanifest.WorkPolicy{{Name: "document-index", MaxRunningPerKey: 1}},
		Triggers: []gregalemanifest.Trigger{{
			Kind: gregalemanifest.TriggerKindKafka, App: app.Slug, Slug: "document-edits",
			Config: map[string]any{
				"brokers": []string{"localhost:9092"}, "topic": "documents", "group": "indexers",
			},
			WorkPolicy: "document-index", WorkKey: "document_id", WorkFairnessKey: "tenant_id",
		}},
	}
	if err := manifest.ValidateForPlan(acct.Plan); err != nil {
		t.Fatal(err)
	}
	staged, problem := srv.applySourceRefManifest(ctx, acct, app, manifest, "", true)
	if problem != nil {
		t.Fatalf("apply manifest: %s", problem.Detail)
	}
	triggers, err := store.ListTriggersForApp(ctx, app.ID)
	if err != nil || len(triggers) != 1 || !triggers[0].Enabled {
		t.Fatalf("triggers = %+v, %v", triggers, err)
	}
	binding, err := store.TriggerWorkBindingByID(ctx, triggers[0].ID.String())
	if err != nil || binding == nil || binding.PolicyName != "document-index" ||
		binding.KeySelector != "document_id" || binding.FairnessSelector != "tenant_id" {
		t.Fatalf("binding = %+v, %v", binding, err)
	}
	second, problem := srv.applySourceRefManifest(ctx, acct, app, manifest, "", true)
	if problem != nil || sourceRefManifestNeedsRollback(second) {
		t.Fatalf("idempotent apply = %+v, %+v", second, problem)
	}
	updated := *manifest
	updated.Triggers = append([]gregalemanifest.Trigger(nil), manifest.Triggers...)
	updated.Triggers[0].WorkKey = "new_document_id"
	change, problem := srv.applySourceRefManifest(ctx, acct, app, &updated, "", true)
	if problem != nil || len(change.triggerWorkChanges) != 1 {
		t.Fatalf("binding update = %+v, %+v", change, problem)
	}
	binding, err = store.TriggerWorkBindingByID(ctx, triggers[0].ID.String())
	if err != nil || binding == nil || binding.KeySelector != "new_document_id" {
		t.Fatalf("updated binding = %+v, %v", binding, err)
	}
	if err := srv.rollbackSourceRefManifest(ctx, change); err != nil {
		t.Fatalf("binding rollback: %v", err)
	}
	binding, err = store.TriggerWorkBindingByID(ctx, triggers[0].ID.String())
	if err != nil || binding == nil || binding.KeySelector != "document_id" {
		t.Fatalf("restored binding = %+v, %v", binding, err)
	}
	if err := srv.rollbackSourceRefManifest(ctx, staged); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	triggers, err = store.ListTriggersForApp(ctx, app.ID)
	if err != nil || len(triggers) != 0 {
		t.Fatalf("triggers after rollback = %+v, %v", triggers, err)
	}
	if _, err := store.AppWorkPolicyByName(ctx, app.ID, "document-index"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("policy after rollback = %v", err)
	}
}
