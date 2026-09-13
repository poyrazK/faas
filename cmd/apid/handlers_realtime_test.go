package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedRealtimeEndpointLifecycle(t *testing.T) {
	teardown := withTestRecipient(t)
	t.Cleanup(teardown)
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "rt-api")
	create := api.CreateManagedRealtimeEndpointRequest{
		CallbackURL:       "https://example.com/callback",
		CallbackAuthToken: "callback-secret",
		AuthToken:         "client-secret",
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/rt-api/realtime/endpoints", create, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body)
	}
	var out api.ManagedRealtimeEndpointResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID == "" || out.ConnectPath != api.DefaultRealtimeConnectPath || out.AuthTokenMasked != api.RealtimeSecretMasked {
		t.Fatalf("unexpected create response: %+v", out)
	}
	if strings.Contains(rec.Body.String(), "callback-secret") || strings.Contains(rec.Body.String(), "client-secret") {
		t.Fatal("realtime credentials leaked in response")
	}
	row, err := e.store.ManagedRealtimeEndpointByID(context.Background(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.CallbackAuthTokenSealed) == 0 || strings.Contains(string(row.CallbackAuthTokenSealed), "callback-secret") {
		t.Fatal("callback credential was not sealed")
	}
	list := e.do(t, http.MethodGet, "/v1/apps/rt-api/realtime/endpoints", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", list.Code, list.Body)
	}
	var rows []api.ManagedRealtimeEndpointResponse
	if err := json.Unmarshal(list.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("list response rows=%d err=%v", len(rows), err)
	}
	enabled := false
	updated := e.do(t, http.MethodPatch, "/v1/apps/rt-api/realtime/endpoints/"+out.ID, api.UpdateManagedRealtimeEndpointRequest{Enabled: &enabled}, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", updated.Code, updated.Body)
	}
	deleted := e.do(t, http.MethodDelete, "/v1/apps/rt-api/realtime/endpoints/"+out.ID, nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", deleted.Code, deleted.Body)
	}
}

func TestManagedRealtimeEndpointPlanGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	// The gate must run before slug loading, preserving the existing paid-only
	// resource posture and avoiding a slug-existence oracle for Free accounts.
	rec := e.do(t, http.MethodPost, "/v1/apps/missing/realtime/endpoints", api.CreateManagedRealtimeEndpointRequest{}, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code != api.CodePlanRealtimeNotAllowed {
		t.Fatalf("problem=%+v err=%v", problem, err)
	}
}
