package incidents

// adr: 20261031144636 — a purged app's incidents outlive it: listed and
// readable with no app slug, unless the caller may not read the account's
// own apps.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPurgedAppIncidentsStayReadable(t *testing.T) {
	f := newFixture(t)
	f.setState(t, state.AlertStateFiring, f.now.Add(-10*time.Minute))
	if err := f.store.DeleteAppPermanently(context.Background(), f.rule.AppID); err != nil {
		t.Fatal(err)
	}

	var resp api.IncidentsResponse
	if code := get(t, f.svc.List, f.acct, "/v1/incidents", "", &resp); code != http.StatusOK || len(resp.Incidents) != 1 {
		t.Fatalf("list after the app was purged = %d %+v; want its incident", code, resp)
	}
	inc := resp.Incidents[0]
	if inc.App != "" || inc.Title != f.rule.Name || inc.Status != api.IncidentStatusResolved {
		t.Fatalf("incident = %+v; want it resolved, with the rule's name and no app", inc)
	}
	var detail api.IncidentDetail
	if code := get(t, f.svc.Get, f.acct, "/v1/incidents/"+inc.ID, inc.ID, &detail); code != http.StatusOK || detail.Incident.ID != inc.ID {
		t.Fatalf("detail = %d %+v", code, detail)
	}

	hidden := *f.svc
	hidden.Host = fakeHost{store: f.store, window: f.win, hidden: map[string]bool{f.rule.AppID: true}}
	if code := get(t, hidden.Get, f.acct, "/v1/incidents/"+inc.ID, inc.ID, nil); code != http.StatusNotFound {
		t.Fatalf("detail for a caller who may not read the account's apps = %d; want 404", code)
	}
}
