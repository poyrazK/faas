package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/role"
)

func TestObsNotificationSuppressionExactEpisode(t *testing.T) {
	e := gapEnv(t)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	labels := map[string]string{"alertname": "FaasApiDown", "severity": "page", "component": "apid"}
	groups := []promql.AlertRuleGroup{{Name: "faas", File: "/etc/prometheus/faas.rules.yml", Rules: []promql.AlertRule{{Type: "alerting", Name: "FaasApiDown", Query: "up == 0", Health: "ok", State: "firing", LastEvaluation: now, Labels: map[string]string{"severity": "page"}, Alerts: []promql.Alert{{State: "firing", ActiveAt: now.Add(-time.Minute), Labels: labels}}}}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/rules":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": groups}})
		case "/api/v2/alerts":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"labels": labels, "startsAt": now.Add(-time.Minute).Truncate(time.Millisecond), "endsAt": now.Add(time.Minute).Truncate(time.Millisecond), "updatedAt": now.Truncate(time.Millisecond), "status": map[string]any{"state": "suppressed", "silencedBy": []string{"private-silence-id"}, "inhibitedBy": []string{}}}})
		default:
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	(*e.S()).WithStatusCache(srv.URL, "").WithAlertmanagerObservability(srv.URL)
	store := *(*e.S()).Store()
	if err := operatorobs.CollectAlertsOnce(t.Context(), store, promql.NewClient(srv.URL, srv.Client()), now); err != nil {
		t.Fatal(err)
	}
	rec := e.DoOperator(t, "GET", "/v1/admin/obs/incidents?type=platform_alert", nil, nil)
	var inbox api.ObsIncidentListResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &inbox) != nil || len(inbox.Items) != 1 {
		t.Fatalf("inbox: %d %s", rec.Code, rec.Body)
	}
	rec = e.DoOperator(t, "GET", inbox.Items[0].EvidencePath, nil, nil)
	var evidence api.ObsIncidentEvidenceResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &evidence) != nil || evidence.Suppression == nil || evidence.Suppression.State != "suppressed" || evidence.Suppression.Silenced == nil || !*evidence.Suppression.Silenced || evidence.Suppression.Inhibited == nil || *evidence.Suppression.Inhibited || len(evidence.UnavailableSources) != 0 {
		t.Fatalf("suppression: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "private-silence-id") || strings.Contains(rec.Body.String(), srv.URL) {
		t.Fatal("private source metadata leaked")
	}
}

func TestObsNotificationHealthReadAndAuthorization(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.NotificationHealthPath
	rec := e.Do(t, "GET", path, nil, nil)
	var out api.ObsNotificationHealth
	if json.Unmarshal(rec.Body.Bytes(), &out) != nil || rec.Code != 200 || out.State != "partial" || out.AlertmanagerAPI.State != "disabled" || out.Integrations == nil || out.Signals["alertmanager_failures"].Value != nil {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body)
	}
	for _, query := range []string{"?since=now", "?url=http://external.example", "?"} {
		if rec := e.Do(t, "GET", path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("query: %d %s", rec.Code, rec.Body)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private provider failure", 503) }))
	defer srv.Close()
	(*e.S()).WithAlertmanagerObservability(srv.URL)
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), srv.URL) || !strings.Contains(rec.Body.String(), "http_error") {
		t.Fatalf("partial: %d %s", rec.Code, rec.Body)
	}
	now := time.Now().UTC().Add(-time.Second)
	episode := seedObsAlert(t, *(*e.S()).Store(), now, true)
	rec = e.DoOperator(t, "GET", "/v1/admin/obs/incidents/platform_alert/"+episode.ID+"/evidence", nil, nil)
	var evidence api.ObsIncidentEvidenceResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &evidence) != nil || evidence.Suppression == nil || evidence.Suppression.Silenced != nil || evidence.Suppression.State != "unavailable" {
		t.Fatalf("retained evidence: %d %s", rec.Code, rec.Body)
	}
	customer := apidserver.NewObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	if rec := customer.Do(t, "GET", path, nil, nil); rec.Code != 403 {
		t.Fatalf("scope: %d %s", rec.Code, rec.Body)
	}
	nonAllowlisted := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "rogue@faas.dev")
	if rec := nonAllowlisted.Do(t, "GET", path, nil, nil); rec.Code != 403 {
		t.Fatalf("allowlist: %d %s", rec.Code, rec.Body)
	}
	h, account, manager, sessionID := apidserver.SetupMW(t, api.PlanPro, true)
	pending := apidserver.ReissueWithMFAFlag(t, manager, sessionID, account.ID, true)
	rec = apidserver.CookieDo(t, h, pending, http.MethodGet, path, nil)
	var problem api.Problem
	if rec.Code != 403 || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("MFA: %d %s", rec.Code, rec.Body)
	}
}

func TestObsAlertmanagerRoleDefault(t *testing.T) {
	for _, tc := range []struct {
		boxRole          role.Role
		configured, want string
	}{
		{role.RoleControlPlane, "", "http://127.0.0.1:9094"}, {role.RoleSingleBox, "", ""}, {role.RoleControlPlane, "disabled", "disabled"}, {role.RoleControlPlane, "http://127.0.0.1:19094", "http://127.0.0.1:19094"},
	} {
		if got := apidserver.ResolveAlertmanagerURL(func(string) string { return tc.configured }, tc.boxRole); got != tc.want {
			t.Fatalf("URL %s: %s", tc.boxRole, got)
		}
	}
}
