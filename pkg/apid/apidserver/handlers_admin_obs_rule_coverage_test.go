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
)

func TestObsRuleCoverageSelectionSourceAndReadOnlyHistory(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.RuleCoveragePath
	store := *(*e.S()).Store()
	episode := seedObsAlert(t, store, time.Now().UTC().Add(-time.Second), true)
	prior, _, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out api.ObsRuleCoverage
	rec := e.Do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "unknown" || out.Reason != "expected_set_not_selected" || out.ExpectedSet != nil || out.Totals != nil || len(out.AvailableSets) != 2 {
		t.Fatalf("unselected: %d %s", rec.Code, rec.Body)
	}
	selected := path + "?expected_set=gregale-core-v1"
	rec = e.Do(t, "GET", selected, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Reason != "prometheus_not_configured" || out.ExpectedSet == nil {
		t.Fatalf("unconfigured: %d %s", rec.Code, rec.Body)
	}
	for _, query := range []string{"?", "?expected_set=", "?expected_set=unknown", "?expected_set=gregale-core-v1&expected_set=gregale-full-v1", "?expected_set=gregale-core-v1&url=https://private.example", "?limit=1"} {
		if rec := e.Do(t, "GET", path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("query accepted: %d %s", rec.Code, rec.Body)
		}
	}
	mode := "empty"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.String() != "/api/v1/rules?type=alert" {
			t.Errorf("source: %s %s", r.Method, r.URL)
		}
		if mode == "failure" {
			http.Error(w, "private upstream body", 503)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[]}}`))
	}))
	defer srv.Close()
	(*e.S()).WithStatusCache(srv.URL, "")
	if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 200 || calls != 0 {
		t.Fatal("unselected request read source")
	}
	rec = e.Do(t, "GET", selected, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "degraded" || out.Totals == nil || out.Totals.Missing != out.ExpectedSet.ExpectedRules || out.Totals.ObservedRules != 0 || !out.Truncated {
		t.Fatalf("missing inventory: %d %s", rec.Code, rec.Body)
	}
	mode = "failure"
	rec = e.Do(t, "GET", selected, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "unknown" || out.Reason != "source_unavailable" || out.Totals != nil || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), srv.URL) {
		t.Fatalf("failed source: %d %s", rec.Code, rec.Body)
	}
	after, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil || before != after || !reflect.DeepEqual(prior, current) {
		t.Fatal("coverage read changed collection or episode history")
	}
}

func TestObsRuleCoverageAuthorization(t *testing.T) {
	path := operatorobs.RuleCoveragePath
	for _, tc := range []struct {
		scopes []string
		email  string
	}{
		{api.ScopesReadSurface, "customer@faas.dev"}, {api.ScopesAdminOnly, "rogue@faas.dev"},
	} {
		e := apidserver.NewObsEnv(t, tc.scopes, "ops@faas.dev", tc.email)
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
