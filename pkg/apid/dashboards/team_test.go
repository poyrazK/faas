package dashboards

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261109153000 — a member's dashboard reads an organization app a
// teammate created as the app's owner: its log rows, held under the
// creator's account, are counted; another account's personal app is
// refused.
func TestDashboardReadsATeammatesOrganizationApp(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	svc, acct := newService(true)
	svc.Now = func() time.Time { return now }
	host := svc.Host.(fakeHost)
	creator := uuid.NewString()
	team := state.App{ID: uuid.NewString(), AccountID: creator, OrgID: uuid.NewString(), Slug: "team"}
	host.apps["team"] = team
	if _, err := host.store.InsertLogEvent(context.Background(), state.LogEvent{
		Source: state.LogEventSourceHTTP, Status: 503, Level: "error", Route: "/pay", Message: "POST /pay returned 503",
		OccurredAt: now.Add(-time.Minute), AccountID: creator, AppID: team.ID, SourceEventID: "team-1",
	}); err != nil {
		t.Fatal(err)
	}
	// Another account's personal app is refused when the dashboard is saved.
	if w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"foreign","widgets":[
		{"title":"foreign","kind":"log_count","app":"foreign","query":"status=5xx"}]}`); w.Code == http.StatusCreated {
		t.Fatal("a widget on another account's personal app was saved")
	}
	w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"team","widgets":[
		{"title":"5xx","kind":"log_count","app":"team","query":"status=5xx"}]}`)
	var created struct{ ID string }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	w = call(t, svc.Data, acct, http.MethodGet, created.ID, "")
	var data struct {
		Widgets []struct {
			Value *float64
			Error string
		}
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data.Widgets) == 0 {
		t.Fatalf("data = %d %s", w.Code, w.Body.String())
	}
	if v := data.Widgets[0]; v.Value == nil || *v.Value != 1 {
		t.Fatalf("teammate's widget = %+v (%s); want the creator's log row counted", v, w.Body.String())
	}
}
