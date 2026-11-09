package incidents

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261109120000 — an organization app's incidents are held under the
// account of the member who created it; a teammate who may read the app
// lists and opens them, and its acknowledgement is the one every member
// sees. Another account's personal app stays out of reach.
func TestTeammatesShareAnOrganizationAppsIncidents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orgApp, err := f.store.CreateApp(ctx, state.App{AccountID: f.acct.ID, OrgID: uuid.NewString(), Slug: "team-shop", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := f.store.CreateAlertRule(ctx, state.AlertRule{
		AccountID: f.acct.ID, AppID: orgApp.ID, Name: "team-5xx", Enabled: true, Metric: state.AlertMetricErrorRate,
		Comparison: state.AlertGt, Threshold: 5, WindowSpec: state.AlertWindow5m, WebhookURL: "https://example.com/hook",
		WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetAlertRuleState(ctx, rule.ID, state.AlertStateFiring, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The personal app's rule fires too: the teammate must not see it.
	f.setState(t, state.AlertStateFiring, f.now.Add(-5*time.Minute))
	mate, err := f.store.CreateAccount(ctx, fmt.Sprintf("mate-%d@example.com", time.Now().UnixNano()), api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	host := f.svc.Host.(fakeHost)
	host.team = []state.App{orgApp}
	mates := &Service{Host: host, Enabled: true, Now: f.svc.Now}

	var resp api.IncidentsResponse
	if code := get(t, mates.List, mate, "/v1/incidents", "", &resp); code != http.StatusOK {
		t.Fatalf("teammate list = %d", code)
	}
	var id string
	for _, inc := range resp.Incidents {
		if inc.Source == api.IncidentSourceAlert {
			if inc.Title != "team-5xx" || id != "" {
				t.Fatalf("teammate sees %+v; want only the organization app's incident", resp.Incidents)
			}
			id = inc.ID
		}
	}
	if id == "" {
		t.Fatalf("teammate list = %+v; want the organization app's incident", resp.Incidents)
	}
	var detail api.IncidentDetail
	if code := get(t, mates.Get, mate, "/v1/incidents/"+id, id, &detail); code != http.StatusOK || detail.Incident.App != "team-shop" {
		t.Fatalf("teammate detail = %d %+v", code, detail.Incident)
	}

	ack := httptest.NewRequest(http.MethodPost, "/v1/incidents/"+id+"/acknowledge", nil)
	ack.SetPathValue("id", id)
	ack.Header.Set("X-Test-Actor", "mate@example.com")
	w := httptest.NewRecorder()
	mates.Acknowledge(w, ack, mate)
	if w.Code != http.StatusCreated {
		t.Fatalf("teammate acknowledge = %d %s", w.Code, w.Body.String())
	}
	// The creator sees the teammate's acknowledgement, in the list too.
	if code := get(t, f.svc.Get, f.acct, "/v1/incidents/"+id, id, &detail); code != http.StatusOK || detail.Incident.AcknowledgedBy != "mate@example.com" {
		t.Fatalf("creator detail = %d %+v; want the teammate's acknowledgement", code, detail.Incident)
	}
	var own api.IncidentsResponse
	get(t, f.svc.List, f.acct, "/v1/incidents", "", &own)
	for _, inc := range own.Incidents {
		switch {
		case inc.ID == id && inc.AcknowledgedBy != "mate@example.com":
			t.Fatalf("creator list = %+v; want the teammate's acknowledgement", inc)
		case inc.Source == api.IncidentSourceAlert && inc.Title == "checkout-5xx":
			// The creator's personal app's incident is not a teammate's to open.
			if code := get(t, mates.Get, mate, "/v1/incidents/"+inc.ID, inc.ID, nil); code == http.StatusOK {
				t.Fatal("a teammate opened another account's personal app incident")
			}
		}
	}
}
