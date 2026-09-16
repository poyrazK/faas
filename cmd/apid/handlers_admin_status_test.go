package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicstatus"
)

func TestAdminStatusCreateUpdateResolveAndList(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	now := time.Now().UTC().Truncate(time.Second)
	headers := map[string]string{"Idempotency-Key": "status-create-test"}
	created := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "Elevated API errors", "impact": "degraded",
		"components": []string{"api_console"}, "state": "investigating", "starts_at": now,
		"message": "We are investigating elevated API errors.",
	}, headers)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var event api.PublicStatusEvent
	if err := json.Unmarshal(created.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.ID == "" || event.State != "investigating" || len(event.Updates) != 1 {
		t.Fatalf("created event = %#v", event)
	}

	replay := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "Different retry body", "impact": "major_outage",
		"components": []string{"networking"}, "state": "monitoring", "starts_at": now,
		"message": "Different retry body.",
	}, headers)
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayEvent api.PublicStatusEvent
	if err := json.Unmarshal(replay.Body.Bytes(), &replayEvent); err != nil {
		t.Fatal(err)
	}
	if replayEvent.ID != event.ID {
		t.Fatalf("idempotent IDs = %q and %q", event.ID, replayEvent.ID)
	}

	updated := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents/"+event.ID+"/updates", map[string]any{
		"state": "resolved", "message": "API error rates have recovered.",
	}, map[string]string{"Idempotency-Key": "status-resolve-test"})
	if updated.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", updated.Code, updated.Body.String())
	}

	terminal := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents/"+event.ID+"/updates", map[string]any{
		"state": "monitoring", "message": "Attempting to reopen.",
	}, map[string]string{"Idempotency-Key": "status-reopen-test"})
	assertProblem(t, terminal, http.StatusConflict, "status_terminal_event")

	listed := e.doAdmin(t, http.MethodGet, "/v1/admin/status/incidents", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	var list []api.PublicStatusEvent
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != event.ID {
		t.Fatalf("list = %#v", list)
	}
}

func TestAdminStatusRerateAndCorrectionRoutes(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	now := time.Now().UTC().Truncate(time.Second)
	created := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "Network eror", "impact": "degraded",
		"components": []string{"networking"}, "state": "investigating", "starts_at": now,
		"message": "We are investigatng.",
	}, map[string]string{"Idempotency-Key": "status-correction-create"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var event api.PublicStatusEvent
	if err := json.Unmarshal(created.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	originalUpdatedAt := event.UpdatedAt
	originalPostedAt := event.Updates[0].PostedAt

	rerated := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents/"+event.ID+"/updates", map[string]any{
		"state": "identified", "message": "Deployment failures identified.",
		"impact": "major_outage", "components": []string{"deployments"},
	}, map[string]string{"Idempotency-Key": "status-correction-rerate"})
	if rerated.Code != http.StatusOK {
		t.Fatalf("rerate status=%d body=%s", rerated.Code, rerated.Body.String())
	}
	if err := json.Unmarshal(rerated.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Impact != "major_outage" || len(event.Components) != 1 || event.Components[0] != "deployments" {
		t.Fatalf("rerated event = %#v", event)
	}
	if event.Updates[1].Impact == nil || *event.Updates[1].Impact != "major_outage" || len(event.Updates[1].Components) != 1 {
		t.Fatalf("rerating absent from timeline: %#v", event.Updates[1])
	}
	reratingUpdatedAt := event.UpdatedAt

	correctedTitle := e.doAdmin(t, http.MethodPatch, "/v1/admin/status/incidents/"+event.ID, map[string]any{
		"title": "Network error",
	}, map[string]string{"Idempotency-Key": "status-correction-title"})
	if correctedTitle.Code != http.StatusOK {
		t.Fatalf("title correction status=%d body=%s", correctedTitle.Code, correctedTitle.Body.String())
	}
	if err := json.Unmarshal(correctedTitle.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Title != "Network error" || event.EditedAt == nil || event.UpdatedAt.Equal(originalUpdatedAt) || !event.UpdatedAt.Equal(reratingUpdatedAt) {
		// updated_at should reflect the rerating, while the title correction
		// itself must not move it back or advance it.
		t.Fatalf("corrected event metadata = %#v", event)
	}
	ratingUpdatedAt := event.UpdatedAt
	correctedUpdate := e.doAdmin(t, http.MethodPatch, "/v1/admin/status/incidents/"+event.ID+"/updates/"+event.Updates[0].ID, map[string]any{
		"message": "We are investigating.",
	}, map[string]string{"Idempotency-Key": "status-correction-message"})
	if correctedUpdate.Code != http.StatusOK {
		t.Fatalf("message correction status=%d body=%s", correctedUpdate.Code, correctedUpdate.Body.String())
	}
	if err := json.Unmarshal(correctedUpdate.Body.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Updates[0].Message != "We are investigating." || event.Updates[0].EditedAt == nil || !event.Updates[0].PostedAt.Equal(originalPostedAt) || !event.UpdatedAt.Equal(ratingUpdatedAt) {
		t.Fatalf("corrected timeline metadata = %#v", event)
	}
}

func TestAdminStatusRejectsInvalidComponentAndMissingAuth(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	now := time.Now().UTC()
	bad := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "Bad mapping", "impact": "degraded",
		"components": []string{"database"}, "state": "investigating", "starts_at": now,
		"message": "This component is not public.",
	}, nil)
	assertProblem(t, bad, http.StatusBadRequest, "status_invalid_component")
	invalidEvent := e.doAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "bogus", "title": "Bad event", "impact": "degraded",
		"components": []string{"api_console"}, "state": "investigating", "starts_at": now,
		"message": "This event kind is invalid.",
	}, map[string]string{"Idempotency-Key": "status-invalid-kind"})
	assertProblem(t, invalidEvent, http.StatusBadRequest, publicstatus.CodeInvalidEvent)

	unauthenticated := e.do(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "No session", "impact": "degraded",
		"components": []string{"api_console"}, "state": "investigating", "starts_at": now,
		"message": "Bearer keys cannot publish incidents.",
	}, map[string]string{"Idempotency-Key": "status-bearer-denied"})
	if unauthenticated.Code != http.StatusForbidden {
		t.Fatalf("bearer mutation status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
	bearerList := e.do(t, http.MethodGet, "/v1/admin/status/incidents", nil, nil)
	assertProblem(t, bearerList, http.StatusForbidden, api.CodeForbidden)
}

func TestAdminStatusRoutesRequireOperatorAllowlist(t *testing.T) {
	e := setup(t, api.PlanPro)
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/admin/status/incidents"},
		{http.MethodPost, "/v1/admin/status/incidents"},
		{http.MethodPatch, "/v1/admin/status/incidents/11111111-1111-4111-8111-111111111111"},
		{http.MethodPost, "/v1/admin/status/incidents/11111111-1111-4111-8111-111111111111/updates"},
		{http.MethodPatch, "/v1/admin/status/incidents/11111111-1111-4111-8111-111111111111/updates/22222222-2222-4222-8222-222222222222"},
	}
	for _, route := range paths {
		recorder := e.doAdmin(t, route.method, route.path, nil, nil)
		assertProblem(t, recorder, http.StatusForbidden, "admin_required")
	}
}
