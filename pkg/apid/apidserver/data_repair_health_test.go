package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestOperatorDataRepairHealthAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, allowlist, email string
		scopes                 []string
		status                 int
	}{
		{"operator", "ops@example.com", "ops@example.com", api.ScopesAdminOnly, http.StatusOK},
		{"customer scope", "ops@example.com", "ops@example.com", []string{api.ScopeAppsRead}, http.StatusForbidden},
		{"unlisted admin", "ops@example.com", "other@example.com", api.ScopesAdminOnly, http.StatusForbidden},
		{"no allowlist", "", "ops@example.com", api.ScopesAdminOnly, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.NewObsEnv(t, tc.scopes, tc.allowlist, tc.email)
			w := e.Do(t, http.MethodGet, "/v1/admin/ops/data-repairs?limit=2", nil, nil)
			if w.Code != tc.status {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var h api.DataRepairHealth
			if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
				t.Fatal(err)
			}
			if h.ObservedAt.IsZero() || len(h.Phases) != len(api.DataRepairHealthPhases()) || h.Items == nil || h.Limit != 2 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("health projection: %+v", h)
			}
			w = e.Do(t, http.MethodGet, "/v1/admin/ops/data-repairs?cursor=bad", nil, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("cursor validation: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestOperatorDataRepairHealthUnavailable(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	// The adapter supplies the core Store contract, without the optional
	// repair-health capability. The endpoint must fail rather than infer zero.
	withoutHealth := statetest.NewStore(&struct{ Store state.Store }{Store: *e.Store()})
	if _, ok := any(withoutHealth).(state.DataRepairHealthStore); ok {
		t.Fatal("fixture unexpectedly exposes repair health")
	}
	*(*e.S()).Store() = withoutHealth
	w := e.Do(t, http.MethodGet, "/v1/admin/ops/data-repairs", nil, nil)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), `"held_slots":0`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unavailable observation: %d %s", w.Code, w.Body.String())
	}
}

func TestOperatorDataRepairHealthRequiresMFA(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, true)
	(*e.Srv()).WithAdminAllowlist(e.Acct().Email)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/data-repairs", nil)
	r.AddCookie(e.MfaIssueWithPending(t, true))
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), api.CodeMFARequired) {
		t.Fatalf("MFA-pending session: %d %s", w.Code, w.Body.String())
	}
}
