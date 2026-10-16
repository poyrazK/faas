package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestObsRuleHealthSourceAndReadOnlyHistory(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.RuleHealthPath
	rec := e.Do(t, "GET", path, nil, nil)
	var out api.ObsRuleHealthResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "disabled" || out.Totals != nil || out.Items == nil {
		t.Fatalf("disabled: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
	var health api.ObsHealthResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &health) != nil || health.AlertHistory == nil || health.AlertHistory.DiagnosticsPath != path {
		t.Fatalf("missing discovery: %d %s", rec.Code, rec.Body)
	}
	for _, query := range []string{"?", "?limit=1", "?since=now", "?url=https://private.example"} {
		if rec := e.Do(t, "GET", path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("query: %d %s", rec.Code, rec.Body)
		}
	}
	now := time.Now().UTC().Add(-time.Second)
	store := *(*e.S()).Store()
	episode := seedObsAlert(t, store, now, true)
	before, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	prior, _, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	rule := promql.AlertRule{Type: "alerting", Name: "FaasApiDown", Query: "private query", State: "inactive", Health: "err", LastError: "private evaluation error", LastEvaluation: now, Alerts: []promql.Alert{}, Annotations: map[string]string{"runbook_url": "/docs/runbooks/FaasApiDown.md"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/rules" || r.URL.Query().Get("type") != "alert" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": []promql.AlertRuleGroup{{Name: "faas", File: "/private-directory/faas.rules.yml", Rules: []promql.AlertRule{rule}}}}})
	}))
	defer srv.Close()
	(*e.S()).WithStatusCache(srv.URL, "")
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "degraded" || out.Totals == nil || out.Totals.Failed != 1 || len(out.Items) != 1 || out.Items[0].Pending != nil || out.Items[0].RunbookURL != "/docs/runbooks/FaasApiDown.md" {
		t.Fatalf("diagnostics: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), srv.URL) {
		t.Fatalf("private source data leaked: %s", rec.Body)
	}
	after, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil || !reflect.DeepEqual(prior, current) || before != after {
		t.Fatal("diagnostic read changed collection or episode history")
	}
}

func TestObsRuleHealthAuthorization(t *testing.T) {
	path := operatorobs.RuleHealthPath
	for _, scopes := range []struct {
		values []string
		email  string
	}{
		{api.ScopesReadSurface, "customer@faas.dev"}, {api.ScopesAdminOnly, "rogue@faas.dev"},
	} {
		e := apidserver.NewObsEnv(t, scopes.values, "ops@faas.dev", scopes.email)
		if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 403 {
			t.Fatalf("authorization: %d %s", rec.Code, rec.Body)
		}
	}
	h, account, manager, sessionID := apidserver.SetupMW(t, api.PlanPro, true)
	pending := apidserver.ReissueWithMFAFlag(t, manager, sessionID, account.ID, true)
	rec := apidserver.CookieDo(t, h, pending, http.MethodGet, path, nil)
	var problem api.Problem
	if rec.Code != 403 || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("MFA: %d %s", rec.Code, rec.Body)
	}
}
