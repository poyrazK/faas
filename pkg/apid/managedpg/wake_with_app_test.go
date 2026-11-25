package managedpg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeWakeWithApp struct {
	called  bool
	enabled bool
}

func (f *fakeWakeWithApp) SetWakeWithApp(_ context.Context, _, _ string, enabled bool) error {
	f.called, f.enabled = true, enabled
	return nil
}

// Turning the setting on needs a plan with managed PostgreSQL; turning it
// off never does, so an account that lost the plan can stop the starts.
func TestWakeWithAppPutPlanGateOnlyForOn(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		plan       api.Plan
		code       int
		called     bool
	}{
		{name: "on with the plan", body: `{"enabled":true}`, plan: api.PlanPro, code: http.StatusOK, called: true},
		{name: "on without the plan", body: `{"enabled":true}`, plan: api.Plan("none"), code: http.StatusTeapot},
		{name: "off without the plan", body: `{"enabled":false}`, plan: api.Plan("none"), code: http.StatusOK, called: true},
		{name: "missing enabled", body: `{}`, plan: api.PlanPro, code: http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeWakeWithApp{}
			h := WakeWithApp{Service: service,
				Problem:       func(w http.ResponseWriter, err error) { w.WriteHeader(http.StatusTeapot) },
				NotConfigured: func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }}
			r := httptest.NewRequest(http.MethodPut, "/v1/postgres/databases/orders/wake-with-app", strings.NewReader(tc.body))
			r.SetPathValue("id", "orders")
			w := httptest.NewRecorder()
			h.Put(w, r, state.Account{ID: "acct-1", Plan: tc.plan})
			if w.Code != tc.code || service.called != tc.called {
				t.Fatalf("status = %d called = %v, want %d %v: %s", w.Code, service.called, tc.code, tc.called, w.Body.String())
			}
		})
	}
}

func TestApplyWakeWithAppHidesItOnAlwaysOn(t *testing.T) {
	enabled := map[string]bool{"db-1": true}
	view := api.ManagedPostgresDatabase{ID: "db-1", ScaleToZero: true}
	ApplyWakeWithApp(&view, enabled)
	if !view.WakeWithApp {
		t.Fatal("scale-to-zero opt-in not shown")
	}
	view = api.ManagedPostgresDatabase{ID: "db-1"}
	ApplyWakeWithApp(&view, enabled)
	if view.WakeWithApp {
		t.Fatal("always-on database shows wake_with_app")
	}
}
