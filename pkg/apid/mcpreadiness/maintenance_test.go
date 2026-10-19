package mcpreadiness

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessMaintenanceHTTP(t *testing.T) {
	s := state.NewMemStore()
	a, err := s.CreateAccount(t.Context(), "maintenance-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "maintenance-http", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	audits := []string{}
	h := &Handler{Store: s, Audit: func(_ context.Context, kind string, _ *string, _ map[string]any) { audits = append(audits, kind) }}
	body := `{"scope":"staging","ends_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `","reason":"upgrade"}`
	request := func(method, q, body, id string, account state.Account) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/apps/maintenance-http/mcp-readiness-maintenance"+q, strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		h.Maintenance(w, r, account)
		return w
	}
	w := request("POST", "", body, "", a)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var window api.MCPReadinessMaintenanceWindow
	_ = json.Unmarshal(w.Body.Bytes(), &window)
	if window.CreatedBy != a.ID || len(audits) != 1 {
		t.Fatal("missing actor/audit", window, audits)
	}
	for _, q := range []string{"?scope=", "?scope=staging&scope=production", "?unknown=x", "?limit=0", "?before=bad", "?scope=%zz"} {
		if got := request("GET", q, "", "", a); got.Code != 400 {
			t.Fatal(q, got.Code)
		}
	}
	for _, b := range []string{`{"ends_at":"2020-01-01T00:00:00Z","reason":"past"}`, `{"ends_at":"2099-01-01T00:00:00Z","reason":""}`, strings.TrimSuffix(body, "}") + `,"extra":true}`, body + body} {
		if got := request("POST", "", b, "", a); got.Code != 400 {
			t.Fatal("invalid body", got.Code, b)
		}
	}
	if got := request("GET", "?scope=staging", "", "", a); got.Code != 200 || !strings.Contains(got.Body.String(), window.ID) {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := request("POST", "", `{"reason":"complete"}`, window.ID, a); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}

	expired, err := s.CreateMCPReadinessMaintenance(t.Context(), a.ID, app.ID, api.CreateMCPReadinessMaintenanceRequest{Scope: "staging", EndsAt: time.Now().Add(-time.Hour), Reason: "previous work"}, time.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := request("POST", "", `{"reason":"too late"}`, expired.ID, a); got.Code != 409 || !strings.Contains(got.Body.String(), "Maintenance window expired") {
		t.Fatal("expired cancellation", got.Code, got.Body.String())
	}
	if len(audits) != 2 {
		t.Fatal("missing cancellation audit", audits)
	}
	if got := request("GET", "", "", "", state.Account{ID: "foreign"}); got.Code != 404 {
		t.Fatal("ownership bypass", got.Code)
	}
	if got := request("POST", "?scope=staging", body, "", a); got.Code != 400 {
		t.Fatal("mutation query accepted", got.Code)
	}
}
