package main

// adr: 590

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventStoragePublishBackpressureAndDuplicate(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	request := api.PublishEventRequest{ID: "original", Source: "orders", Type: "created", Data: json.RawMessage(`{}`)}
	rec := e.do(t, "POST", "/v1/events:publish", request, nil)
	var original api.PublishEventResponse
	if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &original) != nil {
		t.Fatalf("initial=%d %s", rec.Code, rec.Body)
	}
	if err := e.store.UpdateAccountPlan(ctx, e.acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	limit := api.MustLimitsFor(api.PlanFree).EventStorage.RetainedEvents
	for n := int64(1); n < limit; n++ {
		payload := []byte(fmt.Sprintf(`{"id":"fill-%d","source":"orders","type":"created","data":{}}`, n))
		if err := e.store.AppendEvent(ctx, "apid", "event.published", &e.acct.ID, payload); err != nil {
			t.Fatal(err)
		}
	}
	request.ID = "rejected"
	rec = e.do(t, "POST", "/v1/events:publish", request, map[string]string{"Idempotency-Key": "storage-retry"})
	var problem api.Problem
	if json.Unmarshal(rec.Body.Bytes(), &problem) != nil || rec.Code != http.StatusTooManyRequests || problem.Code != "event_storage_capacity_exhausted" || problem.Limit == nil || *problem.Limit != limit || problem.Observed == nil || *problem.Observed != limit+1 || problem.DocsURL == "" || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("quota=%d %s", rec.Code, rec.Body)
	}
	request.ID = "original"
	rec = e.do(t, "POST", "/v1/events:publish", request, nil)
	var duplicate api.PublishEventResponse
	if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &duplicate) != nil || !original.AcceptedAt.Equal(duplicate.AcceptedAt) {
		t.Fatalf("duplicate=%d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "GET", "/v1/events/storage", nil, nil)
	var usage api.EventStorageUsageResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &usage) != nil || usage.RetainedEvents != limit || usage.PendingEvents != limit || usage.OldestPendingAt == nil || usage.RetainedBytes == 0 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("usage=%d %s", rec.Code, rec.Body)
	}
	work, err := e.store.ClaimDuePublishedEvent(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(31*24*time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	request.ID = "rejected"
	rec = e.do(t, "POST", "/v1/events:publish", request, map[string]string{"Idempotency-Key": "storage-retry"})
	if rec.Code != 202 {
		t.Fatalf("after prune=%d %s", rec.Code, rec.Body)
	}
}

func TestEventStorageUsageEmpty(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/events/storage", nil, nil)
	var usage api.EventStorageUsageResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &usage) != nil || usage.RetainedEvents != 0 || usage.OldestPendingAt != nil || usage.Limits != api.MustLimitsFor(api.PlanPro).EventStorage {
		t.Fatalf("empty=%d %s", rec.Code, rec.Body)
	}
}

func TestEventStorageBytesProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	err := fmt.Errorf("wrapped: %w", &state.EventStorageCapacityError{Resource: "bytes", Limit: 100, Observed: 101})
	if !writeEventStorageCapacity(rec, err) {
		t.Fatal("did not recognize storage capacity")
	}
	var problem api.Problem
	if json.Unmarshal(rec.Body.Bytes(), &problem) != nil || rec.Code != 429 || problem.LimitBytes == nil || *problem.LimitBytes != 100 || problem.ObservedBytes == nil || *problem.ObservedBytes != 101 || problem.RetryAfterSeconds == nil || *problem.RetryAfterSeconds != 60 {
		t.Fatalf("bytes=%d %s", rec.Code, rec.Body)
	}
}

func TestEventStorageUsageScopesAndTenantIsolation(t *testing.T) {
	for _, scope := range []string{api.ScopeAppsRead, api.ScopeEventsPublish} {
		t.Run(scope, func(t *testing.T) {
			e := setupWithScopes(t, []string{scope})
			rec := e.do(t, "GET", "/v1/events/storage", nil, nil)
			want := 200
			if scope == api.ScopeEventsPublish {
				want = 403
			}
			if rec.Code != want {
				t.Fatalf("scope=%d %s", rec.Code, rec.Body)
			}
		})
	}
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	other, err := e.store.CreateAccount(ctx, "storage-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(ctx, "apid", "event.published", &other.ID, []byte(`{"id":"other","source":"orders","type":"created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/events/storage?account_id="+other.ID, nil, nil)
	var usage api.EventStorageUsageResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &usage) != nil || usage.RetainedEvents != 0 || usage.Limits != api.MustLimitsFor(api.PlanPro).EventStorage {
		t.Fatalf("tenant=%d %s", rec.Code, rec.Body)
	}
}
