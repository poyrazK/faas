package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppRequestAuditAndDiscoveredRoutesAreAccountScoped(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "audit-api")
	now := time.Now().UTC()
	event := state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: app.ID,
		ConsumerKey: state.AnonymousConsumerKey,
		WindowStart: now.Truncate(time.Minute), RequestCount: 1, BillableUnits: 1,
		Audit: &state.RequestAuditEvidence{
			RouteTemplate: "POST /payments/{id}", Method: "POST", HTTPStatus: 201,
			LatencyMS: 381, OccurredAt: now, CommitSHA: "f92c10",
		},
	}
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/audit-api/audit/requests", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("request audit status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Records []state.RequestAuditRecord `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Records) != 1 || out.Records[0].RouteTemplate != "POST /payments/{id}" {
		t.Fatalf("request audit=%+v err=%v", out, err)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/audit-api/discovered-routes", nil, nil)
	if rec.Code != http.StatusOK || !substringContains(rec.Body.String(), "POST /payments/{id}") {
		t.Fatalf("route inventory status=%d body=%s", rec.Code, rec.Body.String())
	}
	other, err := e.store.CreateAccount(context.Background(), "other-audit@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mustSeedAppFor(t, e.store, other.ID, "private-audit-api")
	rec = e.do(t, http.MethodGet, "/v1/apps/private-audit-api/audit/requests", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/private-audit-api/discovered-routes", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account discovery status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppDiscoveredRoutesWithoutRequestAudit(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "discovery-only")
	now := time.Now().UTC()
	event := state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: app.ID,
		ConsumerKey: state.AnonymousConsumerKey, WindowStart: now.Truncate(time.Minute),
		RequestCount: 1, BillableUnits: 1, DiscoveredRoute: "GET /profile/{id}", DiscoveredAt: now,
	}
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/discovery-only/discovered-routes", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("discovery status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Routes []state.DiscoveredAPIRoute `json:"routes"`
		Source string                     `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Routes) != 1 || out.Routes[0].RouteTemplate != event.DiscoveredRoute || out.Source != "usage_outbox" {
		t.Fatalf("discovery response=%+v err=%v", out, err)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/discovery-only/audit/requests", nil, nil)
	if rec.Code != http.StatusOK || !substringContains(rec.Body.String(), "\"records\":[]") {
		t.Fatalf("unexpected exact audit status=%d body=%s", rec.Code, rec.Body.String())
	}
}
