package dashboards

// adr: 20261022173833 — dashboards save validated widget references per
// account: defaults applied, log queries canonical, apps owned, capped.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeHost struct {
	store *state.MemStore
	apps  map[string]state.App // slug → app
}

func (h fakeHost) AppForAccount(_ context.Context, acct state.Account, slug string) (state.App, bool) {
	app, ok := h.apps[slug]
	return app, ok && app.AccountID == acct.ID
}
func (h fakeHost) Dashboards() state.DashboardStore { return h.store }
func (h fakeHost) Logger() *slog.Logger             { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newService(enabled bool) (*Service, state.Account) {
	acct := state.Account{ID: uuid.NewString()}
	other := uuid.NewString()
	host := fakeHost{store: state.NewMemStore(), apps: map[string]state.App{
		"shop":    {ID: uuid.NewString(), AccountID: acct.ID, Slug: "shop"},
		"foreign": {ID: uuid.NewString(), AccountID: other, Slug: "foreign"},
	}}
	return &Service{Host: host, Enabled: enabled}, acct
}

func call(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/v1/dashboards", strings.NewReader(body))
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h(w, r, acct)
	return w
}

func TestDashboardLifecycle(t *testing.T) {
	svc, acct := newService(true)
	body := `{"name":"checkout","widgets":[
		{"title":"p95","kind":"app_metric","app":"shop","metric":"latency_p95_ms"},
		{"title":"5xx","kind":"log_count","app":"shop","query":"status=5xx&route=/checkout","window":"6h"},
		{"title":"queue","kind":"custom_metric","app":"shop","metric":"orders_pending","window":"5m"}]}`
	w := call(t, svc.Create, acct, http.MethodPost, "", body)
	var created api.Dashboard
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	if len(created.Widgets) != 3 || created.Widgets[0].Window != api.DashboardDefaultWindow ||
		created.Widgets[1].Query != "route=%2Fcheckout&status=5xx" {
		t.Fatalf("widgets = %+v, want the default window and a canonical query", created.Widgets)
	}
	if w := call(t, svc.Create, acct, http.MethodPost, "", body); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "dashboard_name_taken") {
		t.Fatalf("duplicate = %d %s", w.Code, w.Body.String())
	}
	var list api.DashboardsResponse
	if w := call(t, svc.List, acct, http.MethodGet, "", ""); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Dashboards) != 1 {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	w = call(t, svc.Replace, acct, http.MethodPut, created.ID, `{"name":"checkout-v2","widgets":[]}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"checkout-v2"`) {
		t.Fatalf("replace = %d %s", w.Code, w.Body.String())
	}
	if w := call(t, svc.Get, acct, http.MethodGet, created.ID, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"widgets":[]`) {
		t.Fatalf("get = %d %s", w.Code, w.Body.String())
	}
	if w := call(t, svc.Get, state.Account{ID: uuid.NewString()}, http.MethodGet, created.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another account's get = %d, want 404", w.Code)
	}
	if w := call(t, svc.Delete, acct, http.MethodDelete, created.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := call(t, svc.Delete, acct, http.MethodDelete, created.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", w.Code)
	}
}

func TestDashboardWidgetValidation(t *testing.T) {
	svc, acct := newService(true)
	for name, widget := range map[string]string{
		"unknown kind":        `{"title":"x","kind":"pie","app":"shop"}`,
		"another account app": `{"title":"x","kind":"app_metric","app":"foreign","metric":"request_count"}`,
		"unknown metric":      `{"title":"x","kind":"app_metric","app":"shop","metric":"cpu"}`,
		"bad window":          `{"title":"x","kind":"app_metric","app":"shop","metric":"request_count","window":"30d"}`,
		"text over a week":    `{"title":"x","kind":"log_count","app":"shop","query":"q=timeout","window":"7d"}`,
		"window in query":     `{"title":"x","kind":"log_count","app":"shop","query":"since=1h"}`,
		"slo without id":      `{"title":"x","kind":"slo","app":"shop"}`,
		"no title":            `{"title":" ","kind":"app_metric","app":"shop","metric":"request_count"}`,
	} {
		if w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"d","widgets":[`+widget+`]}`); w.Code != http.StatusBadRequest {
			t.Errorf("%s: create = %d %s, want 400", name, w.Code, w.Body.String())
		}
	}
	many := strings.Repeat(`{"title":"x","kind":"app_metric","app":"shop","metric":"request_count"},`, api.DashboardWidgetsMax+1)
	if w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"d","widgets":[`+strings.TrimSuffix(many, ",")+`]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("too many widgets = %d, want 400", w.Code)
	}
	if w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"Bad Name","widgets":[]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad name = %d, want 400", w.Code)
	}
	off, acct := newService(false)
	if w := call(t, off.List, acct, http.MethodGet, "", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("gate off = %d, want 503", w.Code)
	}
}
