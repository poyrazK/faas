package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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
	events, err := e.store.ListEvents(context.Background(), e.acct.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var authConfigured bool
	for _, event := range events {
		if event.Kind != "realtime.auth_policy_configured" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data["auth_mode"] != api.RealtimeAuthModeStaticBearer || data["auth_token_configured"] != true {
			t.Fatalf("unexpected auth policy audit data: %+v", data)
		}
		if strings.Contains(string(event.Data), "client-secret") || strings.Contains(string(event.Data), "callback-secret") || strings.Contains(string(event.Data), "sealed") {
			t.Fatalf("auth policy audit leaked credential material: %s", event.Data)
		}
		authConfigured = true
	}
	if !authConfigured {
		t.Fatalf("realtime.auth_policy_configured audit event missing: %+v", events)
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

func TestManagedRealtimeAuthPolicyUpdateIsAuditedWithoutSecrets(t *testing.T) {
	teardown := withTestRecipient(t)
	t.Cleanup(teardown)
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "rt-auth-audit")
	created := e.do(t, http.MethodPost, "/v1/apps/rt-auth-audit/realtime/endpoints", api.CreateManagedRealtimeEndpointRequest{
		CallbackURL:       "https://example.com/callback",
		CallbackAuthToken: "callback-secret",
		AuthToken:         "old-client-secret",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", created.Code, created.Body)
	}
	var endpoint api.ManagedRealtimeEndpointResponse
	if err := json.Unmarshal(created.Body.Bytes(), &endpoint); err != nil {
		t.Fatal(err)
	}
	newToken := "new-client-secret"
	updated := e.do(t, http.MethodPatch, "/v1/apps/rt-auth-audit/realtime/endpoints/"+endpoint.ID, api.UpdateManagedRealtimeEndpointRequest{AuthToken: &newToken}, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", updated.Code, updated.Body)
	}
	events, err := e.store.ListEvents(context.Background(), e.acct.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != "realtime.auth_policy_updated" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data["auth_token_changed"] != true || data["auth_mode"] != api.RealtimeAuthModeStaticBearer {
			t.Fatalf("unexpected auth policy update data: %+v", data)
		}
		if strings.Contains(string(event.Data), "old-client-secret") || strings.Contains(string(event.Data), "new-client-secret") {
			t.Fatalf("auth policy update audit leaked credential material: %s", event.Data)
		}
		return
	}
	t.Fatalf("realtime.auth_policy_updated audit event missing: %+v", events)
}

func TestManagedRealtimeAuthRotationKeepsPreviousCredentialDuringGrace(t *testing.T) {
	teardown := withTestRecipient(t)
	t.Cleanup(teardown)
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "rt-auth-rotate")
	created := e.do(t, http.MethodPost, "/v1/apps/rt-auth-rotate/realtime/endpoints", api.CreateManagedRealtimeEndpointRequest{
		CallbackURL:       "https://example.com/callback",
		CallbackAuthToken: "callback-secret",
		AuthToken:         "old-client-secret",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", created.Code, created.Body)
	}
	var endpoint api.ManagedRealtimeEndpointResponse
	if err := json.Unmarshal(created.Body.Bytes(), &endpoint); err != nil {
		t.Fatal(err)
	}
	grace := int64(3600)
	rotated := e.do(t, http.MethodPost, "/v1/apps/rt-auth-rotate/realtime/endpoints/"+endpoint.ID+"/auth/rotate", api.RotateManagedRealtimeAuthRequest{
		NewAuthToken: "new-client-secret", GracePeriodSeconds: &grace,
	}, nil)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate status %d: %s", rotated.Code, rotated.Body)
	}
	var response api.RotateManagedRealtimeAuthResponse
	if err := json.Unmarshal(rotated.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.EndpointID != endpoint.ID || response.AuthMode != api.RealtimeAuthModeStaticBearer || response.PreviousTokenExpiresAt == nil || *response.PreviousTokenExpiresAt == "" {
		t.Fatalf("unexpected rotation response: %+v", response)
	}
	if strings.Contains(rotated.Body.String(), "old-client-secret") || strings.Contains(rotated.Body.String(), "new-client-secret") {
		t.Fatalf("rotation response leaked credential material: %s", rotated.Body)
	}
	row, err := e.store.ManagedRealtimeEndpointByID(context.Background(), endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.AuthTokenPreviousSealed) == 0 || row.AuthTokenPreviousExpiresAt == nil || !row.AuthTokenPreviousExpiresAt.After(time.Now()) {
		t.Fatalf("rotation state missing active predecessor: %+v", row)
	}
	finalized := e.do(t, http.MethodPost, "/v1/apps/rt-auth-rotate/realtime/endpoints/"+endpoint.ID+"/auth/rotate/finalize", nil, nil)
	if finalized.Code != http.StatusOK {
		t.Fatalf("finalize status %d: %s", finalized.Code, finalized.Body)
	}
	var finalizeResponse api.FinalizeManagedRealtimeAuthResponse
	if err := json.Unmarshal(finalized.Body.Bytes(), &finalizeResponse); err != nil {
		t.Fatal(err)
	}
	if finalizeResponse.EndpointID != endpoint.ID || finalizeResponse.PreviousTokenExpiresAt != nil {
		t.Fatalf("unexpected finalize response: %+v", finalizeResponse)
	}
	row, err = e.store.ManagedRealtimeEndpointByID(context.Background(), endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.AuthTokenPreviousSealed) != 0 || row.AuthTokenPreviousExpiresAt != nil {
		t.Fatalf("finalize did not clear predecessor: %+v", row)
	}
	events, err := e.store.ListEvents(context.Background(), e.acct.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != "realtime.auth_token_rotated" {
			continue
		}
		if strings.Contains(string(event.Data), "old-client-secret") || strings.Contains(string(event.Data), "new-client-secret") {
			t.Fatalf("rotation audit leaked credential material: %s", event.Data)
		}
	}
	var rotatedAudit, finalizedAudit bool
	for _, event := range events {
		rotatedAudit = rotatedAudit || event.Kind == "realtime.auth_token_rotated"
		finalizedAudit = finalizedAudit || event.Kind == "realtime.auth_token_rotation_finalized"
	}
	if !rotatedAudit || !finalizedAudit {
		t.Fatalf("rotation audit events missing: %+v", events)
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
