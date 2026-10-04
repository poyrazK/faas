package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type inventoryReadStore struct {
	*state.MemStore
	objects                                               []state.ObjectStorageBindingInventory
	objectErr, consumerErr, queueErr, outboundErr         error
	objectCalls, consumerCalls, queueCalls, outboundCalls atomic.Int32
}

func (s *inventoryReadStore) ListObjectStorageBindingsForApp(ctx context.Context, accountID, appID, scope string) ([]state.ObjectStorageBindingInventory, error) {
	s.objectCalls.Add(1)
	if s.objectErr != nil {
		return nil, s.objectErr
	}
	if s.objects == nil {
		return s.MemStore.ListObjectStorageBindingsForApp(ctx, accountID, appID, scope)
	}
	var items []state.ObjectStorageBindingInventory
	for _, item := range s.objects {
		if scope == "" || scope == item.Scope {
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *inventoryReadStore) ListQueueBindingConsumersForApp(ctx context.Context, accountID, appID string) ([]state.QueueBindingConsumerInventory, error) {
	s.consumerCalls.Add(1)
	if s.consumerErr != nil {
		return nil, s.consumerErr
	}
	return s.MemStore.ListQueueBindingConsumersForApp(ctx, accountID, appID)
}

func (s *inventoryReadStore) ListQueueBindingsForApp(ctx context.Context, accountID, appID string) ([]state.QueueBinding, error) {
	s.queueCalls.Add(1)
	if s.queueErr != nil {
		return nil, s.queueErr
	}
	return s.MemStore.ListQueueBindingsForApp(ctx, accountID, appID)
}

func (s *inventoryReadStore) ListOutboundAppBindings(ctx context.Context, accountID, appID string) ([]state.OutboundAppBinding, error) {
	s.outboundCalls.Add(1)
	if s.outboundErr != nil {
		return nil, s.outboundErr
	}
	return s.MemStore.ListOutboundAppBindings(ctx, accountID, appID)
}

func seedInventoryQueue(t *testing.T, e testEnv, app state.App, name string, observe bool) state.QueueBinding {
	t.Helper()
	ctx := context.Background()
	binding, err := e.store.CreateQueueBinding(ctx, state.QueueBinding{
		AccountID: e.acct.ID, AppID: app.ID, Name: name, QueueName: name, Mode: "push",
		WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observe {
		if _, err := e.store.UpdateQueueBindingWithConsumer(ctx, e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{}); err != nil {
			t.Fatal(err)
		}
		id, err := queueBindingTriggerID(ctx, e.store, app.ID, binding.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.RecordTriggerConsumerHealth(ctx, id, state.TriggerConsumerHealthObservation{
			LastPollAt: time.Now().UTC(), Error: "PRIVATE_ERROR postgres://user:password@host/db",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return binding
}

func decodeBindingInventory(t *testing.T, rec *httptest.ResponseRecorder) api.AppBindingInventory {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("inventory status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got api.AppBindingInventory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func seedInventoryPostgres(t *testing.T, e testEnv, appID string) {
	t.Helper()
	store, _, databaseID := configureSourceRefManagedPostgres(t, sourceRefTestEnv{acctID: e.acct.ID, srv: e.s})
	for _, input := range []struct{ appID, scope string }{{appID, "production"}, {appID, "staging"}, {uuid.NewString(), "production"}} {
		_, _, err := store.ReserveBinding(context.Background(), managedpostgres.Binding{
			ID: uuid.NewString(), AccountID: e.acct.ID, DatabaseID: databaseID, AppID: input.appID,
			Scope: input.scope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite,
			CredentialGeneration: 1, State: managedpostgres.BindingStateProvisioning,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppBindingInventoryCombinesMetadataAndObservedStatus(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(context.Background(), state.App{AccountID: e.acct.ID, Slug: "inventory", WorkloadClass: state.WorkloadClassWorker,
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared, ServiceBindingTransport: api.ServiceBindingTransportHTTPS,
			ServiceBindings: []api.AppServiceBinding{{Service: "billing", Binding: "GREGALE_SERVICE_BILLING_URL"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	seedInventoryPostgres(t, e, app.ID)
	seedInventoryQueue(t, e, app, "orders", true)
	seedInventoryQueue(t, e, app, "unobserved", false)
	id := uuid.NewString()
	e.store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{ID: id, AccountID: e.acct.ID, Name: "payments", Origin: "https://PRIVATE_ORIGIN.example", Enabled: true, AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1"}})
	if _, err := e.store.BindOutboundIntegration(context.Background(), e.acct.ID, app.ID, id); err != nil {
		t.Fatal(err)
	}
	reads := &inventoryReadStore{MemStore: e.store, objects: []state.ObjectStorageBindingInventory{
		{BucketName: "assets", Scope: "production", Prefix: "GREGALE_S3_ASSETS", Permission: "read", State: "active", RotationPending: true},
		{BucketName: "assets-stage", Scope: "staging", Prefix: "GREGALE_S3_ASSETS", Permission: "read_write", State: "active"},
	}}
	e.s.store = reads
	rec := e.do(t, http.MethodGet, "/v1/apps/inventory/bindings?scope=production", nil, nil)
	got := decodeBindingInventory(t, rec)
	if !got.Complete || got.HasErrors() || got.Scope != "production" || len(got.Bindings) != 6 || got.GeneratedAt.IsZero() {
		t.Fatalf("inventory=%+v", got)
	}
	kinds := make(map[string]bool)
	for _, item := range got.Bindings {
		kinds[item.Type] = true
		if item.VerificationStatus != "unknown" {
			t.Fatalf("unprobed binding marked verified: %+v", item)
		}
		if item.Scope != "app" && item.Scope != "production" {
			t.Fatalf("scope filter leaked: %+v", item)
		}
		if item.Type != api.BindingTypeQueue && (item.RuntimeStatus != "unknown" || item.ObservedAt != nil) {
			t.Fatalf("configured binding marked observed: %+v", item)
		}
		if item.Type == api.BindingTypeQueue && item.Name == "orders" && (item.State != "enabled" || item.RuntimeStatus != "degraded" || item.ObservedAt == nil) {
			t.Fatalf("queue observation=%+v", item)
		}
		if item.Name == "unobserved" && (item.ConsumerState != "not_configured" || item.RuntimeStatus != "unknown" || item.ObservedAt != nil) {
			t.Fatalf("unobserved consumer=%+v", item)
		}
	}
	if len(kinds) != 5 {
		t.Fatalf("kinds=%v", kinds)
	}
	for _, sensitive := range []string{"PRIVATE_ERROR", "password", "PRIVATE_ORIGIN", id, app.ID} {
		if strings.Contains(rec.Body.String(), sensitive) {
			t.Fatalf("inventory leaked %q: %s", sensitive, rec.Body.String())
		}
	}
	if reads.objectCalls.Load() != 1 || reads.queueCalls.Load() != 1 || reads.consumerCalls.Load() != 1 || reads.outboundCalls.Load() != 1 {
		t.Fatal("inventory did not batch app reads")
	}
	all := decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/inventory/bindings", nil, nil))
	if len(all.Bindings) != 8 || all.Scope != "" {
		t.Fatalf("all scopes=%+v", all)
	}
}

func TestAppBindingInventoryPreservesOtherSectionsAndQueueConfiguration(t *testing.T) {
	for _, kind := range []string{"object_storage", "outbound", "queue", "consumer"} {
		t.Run(kind, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			app := createApp(t, e, "partial-inventory")
			seedInventoryQueue(t, e, app, "orders", false)
			reads := &inventoryReadStore{MemStore: e.store}
			failure := errors.New("PRIVATE_ERROR postgres://user:password@host/db")
			switch kind {
			case "object_storage":
				reads.objectErr = failure
			case "outbound":
				reads.outboundErr = failure
			case "queue":
				reads.queueErr = failure
			case "consumer":
				reads.consumerErr = failure
			}
			e.s.store = reads
			rec := e.do(t, http.MethodGet, "/v1/apps/partial-inventory/bindings", nil, nil)
			got := decodeBindingInventory(t, rec)
			if got.Complete || !got.HasErrors() || len(got.Issues) != 2 {
				t.Fatalf("inventory=%+v", got)
			}
			if kind != "queue" && len(got.Bindings) != 1 {
				t.Fatalf("queue configuration lost: %+v", got)
			}
			if kind == "consumer" && (got.Bindings[0].ConsumerState != "unknown" || got.Bindings[0].RuntimeStatus != "unknown") {
				t.Fatalf("failed observation inferred: %+v", got.Bindings[0])
			}
			if strings.Contains(rec.Body.String(), "PRIVATE_ERROR") || strings.Contains(rec.Body.String(), "password") {
				t.Fatalf("raw error leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestAppBindingInventoryPermissionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scopes    []string
		forbidden int
		objects   int32
	}{
		{"app read", []string{api.ScopeAppsRead}, 2, 0},
		{"storage read", []string{api.ScopeAppsRead, api.ScopeStorageRead}, 2, 0},
		{"storage manage", []string{api.ScopeAppsRead, api.ScopeStorageManage}, 1, 1},
		{"PostgreSQL read", []string{api.ScopeAppsRead, api.ScopeManagedPostgresRead}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setupWithScopes(t, tc.scopes)
			app, err := e.store.CreateApp(context.Background(), state.App{AccountID: e.acct.ID, Slug: "scoped-inventory"})
			if err != nil {
				t.Fatal(err)
			}
			reads := &inventoryReadStore{MemStore: e.store, objects: []state.ObjectStorageBindingInventory{{BucketName: "PRIVATE_BUCKET", Scope: "production"}}}
			e.s.store = reads
			got := decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil))
			forbidden := 0
			for _, issue := range got.Issues {
				if issue.Code == "forbidden" {
					forbidden++
				}
			}
			if got.Complete || forbidden != tc.forbidden || reads.objectCalls.Load() != tc.objects {
				t.Fatalf("inventory=%+v forbidden=%d object reads=%d", got, forbidden, reads.objectCalls.Load())
			}
			if tc.objects == 0 && len(got.Bindings) != 0 {
				t.Fatalf("forbidden resource metadata leaked: %+v", got)
			}
		})
	}
}

func TestAppBindingInventoryRejectsCrossAccountAndInvalidScope(t *testing.T) {
	e := setup(t, api.PlanPro)
	other, err := e.store.CreateAccount(context.Background(), "other-inventory@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateApp(context.Background(), state.App{AccountID: other.ID, Slug: "other-inventory"}); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, e.do(t, http.MethodGet, "/v1/apps/other-inventory/bindings", nil, nil), http.StatusNotFound, api.CodeNotFound)
	app := createApp(t, e, "valid-inventory")
	reads := &inventoryReadStore{MemStore: e.store}
	e.s.store = reads
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings?scope=__all__", nil, nil)
	if rec.Code != http.StatusBadRequest || reads.queueCalls.Load() != 0 || reads.objectCalls.Load() != 0 {
		t.Fatalf("invalid scope status=%d body=%s", rec.Code, rec.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/apps/valid-inventory/bindings", nil)
	request = request.WithContext(middleware.WithMFAPending(middleware.WithPrincipal(request.Context(), e.acct, nil, nil), true))
	pending := httptest.NewRecorder()
	e.s.requireMFA(e.s.getAppBindingInventory)(pending, request, e.acct)
	if pending.Code != http.StatusForbidden {
		t.Fatalf("pending MFA status=%d body=%s", pending.Code, pending.Body.String())
	}
}

func TestAppBindingInventoryQueueReadsStayBatched(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := createApp(t, e, "many-bindings")
	for index := range 30 {
		name := fmt.Sprintf("queue-%02d", index)
		if _, err := e.store.CreateQueueBinding(context.Background(), state.QueueBinding{
			AccountID: e.acct.ID, AppID: app.ID, Name: name, QueueName: name,
			Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	reads := &inventoryReadStore{MemStore: e.store}
	e.s.store = reads
	got := decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/many-bindings/bindings", nil, nil))
	if len(got.Bindings) != 30 || got.HasErrors() || reads.queueCalls.Load() != 1 || reads.consumerCalls.Load() != 1 {
		t.Fatalf("inventory=%+v queue reads=%d consumer reads=%d", got, reads.queueCalls.Load(), reads.consumerCalls.Load())
	}
}

func TestBindingInventoryConsumerObservationStates(t *testing.T) {
	now := time.Now().UTC()
	poll := now.Add(-time.Second)
	stale := now.Add(-api.QueueConsumerMaxPollAge - time.Second)
	yes, no := true, false
	for _, tc := range []struct {
		name            string
		enabled         bool
		consumer        state.QueueBindingConsumerInventory
		config, runtime string
	}{
		{"missing projection", true, state.QueueBindingConsumerInventory{}, "not_configured", "unknown"},
		{"unobserved projection", true, state.QueueBindingConsumerInventory{ConsumerEnabled: &yes}, "active", "unknown"},
		{"disabled binding", false, state.QueueBindingConsumerInventory{ConsumerEnabled: &yes, LastPollAt: &poll}, "paused", "unknown"},
		{"disabled consumer", true, state.QueueBindingConsumerInventory{ConsumerEnabled: &no, LastPollAt: &poll}, "paused", "unknown"},
		{"healthy", true, state.QueueBindingConsumerInventory{ConsumerEnabled: &yes, LastPollAt: &poll, LastSuccessAt: &poll}, "active", "healthy"},
		{"stale", true, state.QueueBindingConsumerInventory{ConsumerEnabled: &yes, LastPollAt: &stale}, "active", "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := inventoryItem(api.BindingTypeQueue, "orders", "worker", "app", "push", "enabled")
			applyConsumerInventory(&item, tc.enabled, tc.consumer, now)
			if item.ConsumerState != tc.config || item.RuntimeStatus != tc.runtime || item.VerificationStatus != "unknown" {
				t.Fatalf("status=%+v", item)
			}
		})
	}
}
