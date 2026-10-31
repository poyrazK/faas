package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/publicstatus"
)

func TestAdminStatusCreateUpdateResolveAndList(t *testing.T) {
	withIdempotencyHostKey(t)
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithAdminAllowlist((*e.Acct()).Email)
	now := time.Now().UTC().Truncate(time.Second)
	headers := map[string]string{"Idempotency-Key": "status-create-test"}
	created := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
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

	// ADR-20261022204036: the key is bound to the first request; another
	// body is refused, and the original request still replays.
	reused := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "Different retry body", "impact": "major_outage",
		"components": []string{"networking"}, "state": "monitoring", "starts_at": now,
		"message": "Different retry body.",
	}, headers)
	apidserver.AssertProblem(t, reused, http.StatusUnprocessableEntity, api.CodeIdempotencyKeyReused)
	replay := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "Elevated API errors", "impact": "degraded",
		"components": []string{"api_console"}, "state": "investigating", "starts_at": now,
		"message": "We are investigating elevated API errors.",
	}, headers)
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayEvent api.PublicStatusEvent
	if err := json.Unmarshal(replay.Body.Bytes(), &replayEvent); err != nil {
		t.Fatal(err)
	}
	if replayEvent.ID != event.ID {
		t.Fatalf("idempotent IDs = %q and %q", event.ID, replayEvent.ID)
	}

	updated := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents/"+event.ID+"/updates", map[string]any{
		"state": "resolved", "message": "API error rates have recovered.",
	}, map[string]string{"Idempotency-Key": "status-resolve-test"})
	if updated.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", updated.Code, updated.Body.String())
	}

	terminal := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents/"+event.ID+"/updates", map[string]any{
		"state": "monitoring", "message": "Attempting to reopen.",
	}, map[string]string{"Idempotency-Key": "status-reopen-test"})
	apidserver.AssertProblem(t, terminal, http.StatusConflict, "status_terminal_event")

	listed := e.DoAdmin(t, http.MethodGet, "/v1/admin/status/incidents", nil, nil)
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
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithAdminAllowlist((*e.Acct()).Email)
	now := time.Now().UTC().Truncate(time.Second)
	created := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
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

	rerated := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents/"+event.ID+"/updates", map[string]any{
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

	correctedTitle := e.DoAdmin(t, http.MethodPatch, "/v1/admin/status/incidents/"+event.ID, map[string]any{
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
	correctedUpdate := e.DoAdmin(t, http.MethodPatch, "/v1/admin/status/incidents/"+event.ID+"/updates/"+event.Updates[0].ID, map[string]any{
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
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithAdminAllowlist((*e.Acct()).Email)
	now := time.Now().UTC()
	bad := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "Bad mapping", "impact": "degraded",
		"components": []string{"database"}, "state": "investigating", "starts_at": now,
		"message": "This component is not public.",
	}, nil)
	apidserver.AssertProblem(t, bad, http.StatusBadRequest, "status_invalid_component")
	invalidEvent := e.DoAdmin(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "bogus", "title": "Bad event", "impact": "degraded",
		"components": []string{"api_console"}, "state": "investigating", "starts_at": now,
		"message": "This event kind is invalid.",
	}, map[string]string{"Idempotency-Key": "status-invalid-kind"})
	apidserver.AssertProblem(t, invalidEvent, http.StatusBadRequest, publicstatus.CodeInvalidEvent)

	unauthenticated := e.Do(t, http.MethodPost, "/v1/admin/status/incidents", map[string]any{
		"kind": "incident", "title": "No session", "impact": "degraded",
		"components": []string{"api_console"}, "state": "investigating", "starts_at": now,
		"message": "Bearer keys cannot publish incidents.",
	}, map[string]string{"Idempotency-Key": "status-bearer-denied"})
	if unauthenticated.Code != http.StatusForbidden {
		t.Fatalf("bearer mutation status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
	bearerList := e.Do(t, http.MethodGet, "/v1/admin/status/incidents", nil, nil)
	apidserver.AssertProblem(t, bearerList, http.StatusForbidden, api.CodeForbidden)
}

func TestAdminStatusRoutesRequireOperatorAllowlist(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
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
		recorder := e.DoAdmin(t, route.method, route.path, nil, nil)
		apidserver.AssertProblem(t, recorder, http.StatusForbidden, "admin_required")
	}
}
