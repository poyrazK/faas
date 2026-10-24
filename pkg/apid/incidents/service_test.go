package incidents

// adr: 20261022193512 — one list of alert episodes, route monitor incidents
// and platform incidents, open first; a detail carries fires, log context,
// status updates and the app's changes.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeHost struct {
	store   *state.MemStore
	changes bool
	window  *[2]time.Time
}

func (h fakeHost) LoadApp(w http.ResponseWriter, _ *http.Request, acct state.Account, slug string) (state.App, bool) {
	app, err := h.store.AppBySlug(context.Background(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, nil)
		return state.App{}, false
	}
	return app, true
}
func (h fakeHost) Store() Store                                  { return h.store }
func (h fakeHost) Actor(r *http.Request, _ state.Account) string { return r.Header.Get("X-Test-Actor") }
func (h fakeHost) Logger() *slog.Logger                          { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (h fakeHost) Changes(_ context.Context, _ state.Account, app state.App, since, until time.Time) (api.AppChangeTimelineResponse, bool) {
	*h.window = [2]time.Time{since, until}
	return api.AppChangeTimelineResponse{AppID: app.ID, Events: []api.AppChangeEvent{
		{At: since.Add(30 * time.Minute), Source: api.ChangeSourceDeployment, Kind: "deploy.created", Summary: "Deployment created"},
	}, UnavailableSources: []string{api.ChangeSourceHealth}}, h.changes
}

type fixture struct {
	svc   *Service
	store *state.MemStore
	acct  state.Account
	rule  state.AlertRule
	now   time.Time
	win   *[2]time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, fmt.Sprintf("incidents-%d@example.com", time.Now().UnixNano()), api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(ctx, state.AlertRule{
		AccountID: acct.ID, AppID: app.ID, Name: "checkout-5xx", Enabled: true, Metric: state.AlertMetricErrorRate,
		Comparison: state.AlertGt, Threshold: 5, WindowSpec: state.AlertWindow5m, WebhookURL: "https://example.com/hook",
		WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	win := &[2]time.Time{}
	svc := &Service{Host: fakeHost{store: store, changes: true, window: win}, Enabled: true, Now: func() time.Time { return now }}
	return fixture{svc: svc, store: store, acct: acct, rule: rule, now: now, win: win}
}

func (f fixture) setState(t *testing.T, to state.AlertState, at time.Time) {
	t.Helper()
	if _, err := f.store.SetAlertRuleState(context.Background(), f.rule.ID, to, at); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) fire(t *testing.T, at time.Time, payload string) {
	t.Helper()
	if _, won, err := f.store.ClaimAlertFire(context.Background(), f.rule.ID, f.rule.ID+at.String(), []byte(payload), 9.5, at); err != nil || !won {
		t.Fatalf("ClaimAlertFire = %v, %v", won, err)
	}
}

func get(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, target, id string, out any) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h(w, r, acct)
	if out != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
	}
	return w.Code
}

func TestListMergesSourcesOpenFirst(t *testing.T) {
	f := newFixture(t)
	// A resolved episode two hours ago, then an open one from ten minutes ago.
	f.setState(t, state.AlertStateFiring, f.now.Add(-2*time.Hour))
	f.setState(t, state.AlertStateOk, f.now.Add(-90*time.Minute))
	f.setState(t, state.AlertStateFiring, f.now.Add(-10*time.Minute))
	started := f.now.Add(-time.Hour)
	if _, err := f.store.CreatePublicStatusEvent(context.Background(), state.StatusEventCreate{
		IdempotencyKey: "incident-1", Kind: publicstatus.KindIncident, Title: "Elevated wake latency",
		Impact: publicstatus.StateDegraded, Components: []publicstatus.Component{publicstatus.ComponentAppExecution},
		State: publicstatus.LifecycleInvestigating, StartsAt: &started, Message: "Looking into it",
	}); err != nil {
		t.Fatal(err)
	}

	var resp api.IncidentsResponse
	if code := get(t, f.svc.List, f.acct, "/v1/incidents", "", &resp); code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	if len(resp.Incidents) != 3 || len(resp.UnavailableSources) != 0 || resp.Truncated {
		t.Fatalf("list = %+v", resp)
	}
	want := []struct{ source, status, title string }{
		{api.IncidentSourceAlert, api.IncidentStatusOpen, "checkout-5xx"},
		{api.IncidentSourcePlatform, api.IncidentStatusOpen, "Elevated wake latency"},
		{api.IncidentSourceAlert, api.IncidentStatusResolved, "checkout-5xx"},
	}
	for i, w := range want {
		got := resp.Incidents[i]
		if got.Source != w.source || got.Status != w.status || got.Title != w.title {
			t.Fatalf("incident %d = %+v, want %+v", i, got, w)
		}
	}
	if alert := resp.Incidents[0]; alert.App != "shop" || alert.RuleID != f.rule.ID || alert.Metric != string(state.AlertMetricErrorRate) {
		t.Fatalf("alert incident = %+v", alert)
	}
	if platform := resp.Incidents[1]; platform.Impact != string(publicstatus.StateDegraded) || !platform.OpenedAt.Equal(started) {
		t.Fatalf("platform incident = %+v", platform)
	}

	tests := []struct {
		name   string
		target string
		want   int
		code   int
	}{
		{"open only", "/v1/incidents?status=open", 2, http.StatusOK},
		{"window excludes the older resolved episode", "/v1/incidents?since=" + f.now.Add(-80*time.Minute).Format(time.RFC3339), 2, http.StatusOK},
		{"app filter keeps platform incidents", "/v1/incidents?app=shop", 3, http.StatusOK},
		{"unknown app", "/v1/incidents?app=nope", 0, http.StatusNotFound},
		{"bad status", "/v1/incidents?status=closed", 0, http.StatusBadRequest},
		{"bad since", "/v1/incidents?since=yesterday", 0, http.StatusBadRequest},
		{"since past the max window", "/v1/incidents?since=" + f.now.Add(-api.IncidentsMaxWindow-time.Hour).Format(time.RFC3339), 0, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var resp api.IncidentsResponse
			if code := get(t, f.svc.List, f.acct, tc.target, "", &resp); code != tc.code || len(resp.Incidents) != tc.want {
				t.Fatalf("%s = %d with %d incidents, want %d with %d", tc.target, code, len(resp.Incidents), tc.code, tc.want)
			}
		})
	}
}

func TestDetailCarriesFiresLogContextAndChanges(t *testing.T) {
	f := newFixture(t)
	opened := f.now.Add(-20 * time.Minute)
	f.fire(t, opened.Add(-time.Hour), `{"event":"alert.fired"}`) // an earlier episode's fire
	f.setState(t, state.AlertStateFiring, opened)
	f.fire(t, opened, `{"event":"alert.fired"}`)
	f.fire(t, opened.Add(10*time.Minute), `{"event":"alert.fired","log_context":{"window":"15m","errors":4}}`)
	f.setState(t, state.AlertStateOk, opened.Add(15*time.Minute))

	var list api.IncidentsResponse
	get(t, f.svc.List, f.acct, "/v1/incidents", "", &list)
	if len(list.Incidents) != 1 {
		t.Fatalf("list = %+v", list)
	}
	var detail api.IncidentDetail
	if code := get(t, f.svc.Get, f.acct, "/v1/incidents/x", list.Incidents[0].ID, &detail); code != http.StatusOK {
		t.Fatalf("detail = %d", code)
	}
	if detail.Incident.Status != api.IncidentStatusResolved || len(detail.Fires) != 2 || !detail.Fires[0].At.Equal(opened.Add(10*time.Minute)) {
		t.Fatalf("detail = %+v; want the episode's two fires, newest first", detail)
	}
	if string(detail.LogContext) != `{"window":"15m","errors":4}` {
		t.Fatalf("log context = %s", detail.LogContext)
	}
	if len(detail.Changes) != 1 || len(detail.UnavailableSources) != 1 || detail.UnavailableSources[0] != "changes."+api.ChangeSourceHealth {
		t.Fatalf("changes = %+v, unavailable = %v", detail.Changes, detail.UnavailableSources)
	}
	if since, until := f.win[0], f.win[1]; !since.Equal(opened.Add(-api.IncidentChangeLookback)) || !until.Equal(opened.Add(15*time.Minute+time.Second)) {
		t.Fatalf("changes window = %v..%v", since, until)
	}

	f.svc.Host = fakeHost{store: f.store, window: f.win}
	detail = api.IncidentDetail{}
	get(t, f.svc.Get, f.acct, "/v1/incidents/x", list.Incidents[0].ID, &detail)
	if len(detail.Changes) != 0 || len(detail.UnavailableSources) != 1 || detail.UnavailableSources[0] != "changes" {
		t.Fatalf("with the change timeline off: changes = %+v, unavailable = %v", detail.Changes, detail.UnavailableSources)
	}

	other, err := f.store.CreateAccount(context.Background(), "other-incidents@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{list.Incidents[0].ID, "alert-not-a-uuid", "route-" + f.rule.ID, "platform-x", "bogus", "alert-"} {
		acct := f.acct
		if id == list.Incidents[0].ID {
			acct = other
		}
		if code := get(t, f.svc.Get, acct, "/v1/incidents/x", id, nil); code != http.StatusNotFound {
			t.Errorf("detail(%q) = %d, want 404", id, code)
		}
	}
}

func TestPlatformDetailAndGate(t *testing.T) {
	f := newFixture(t)
	started := f.now.Add(-30 * time.Minute)
	ev, err := f.store.CreatePublicStatusEvent(context.Background(), state.StatusEventCreate{
		IdempotencyKey: "incident-2", Kind: publicstatus.KindIncident, Title: "Deploys delayed",
		Impact: publicstatus.StatePartialOutage, Components: []publicstatus.Component{publicstatus.ComponentDeployments},
		State: publicstatus.LifecycleIdentified, StartsAt: &started, Message: "Builder queue backed up",
	})
	if err != nil {
		t.Fatal(err)
	}
	var detail api.IncidentDetail
	if code := get(t, f.svc.Get, f.acct, "/v1/incidents/x", "platform-"+ev.PublicID, &detail); code != http.StatusOK {
		t.Fatalf("platform detail = %d", code)
	}
	if detail.Incident.Title != "Deploys delayed" || len(detail.Updates) != 1 || detail.Updates[0].Message != "Builder queue backed up" || len(detail.Changes) != 0 {
		t.Fatalf("platform detail = %+v", detail)
	}
	f.svc.Enabled = false
	if code := get(t, f.svc.List, f.acct, "/v1/incidents", "", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("gated list = %d, want 503", code)
	}
	if code := get(t, f.svc.Get, f.acct, "/v1/incidents/x", "platform-"+ev.PublicID, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("gated detail = %d, want 503", code)
	}
}
