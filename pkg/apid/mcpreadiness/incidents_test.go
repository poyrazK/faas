package mcpreadiness

import (
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPReadinessIncidentsHTTP(t *testing.T) {
	s := state.NewMemStore()
	a, err := s.CreateAccount(t.Context(), "incident-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "incident-http", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: s}
	for _, tc := range []struct {
		q       string
		account state.Account
		status  int
	}{{"", a, 200}, {"?scope=staging&limit=1", a, 200}, {"?scope=", a, 400}, {"?scope=__all__", a, 400}, {"?scope=staging&scope=default", a, 400}, {"?before=bad", a, 400}, {"?unknown=1", a, 400}, {"?limit=51", a, 400}, {"?limit=0", a, 400}, {"?scope=%zz", a, 400}, {"", state.Account{ID: "foreign"}, 404}} {
		r := httptest.NewRequest("GET", "/v1/apps/incident-http/mcp-readiness-incidents"+tc.q, nil)
		r.SetPathValue("slug", app.Slug)
		w := httptest.NewRecorder()
		h.Incidents(w, r, tc.account)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc.q, w.Code, w.Body.String())
		}
	}
}
