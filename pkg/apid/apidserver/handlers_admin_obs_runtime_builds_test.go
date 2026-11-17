package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestObsRuntimeBuildsProtectedRead(t *testing.T) {
	e := gapEnv(t)
	query := "?release=" + strings.Repeat("a", 40) + "&after=" + time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	path := operatorobs.RuntimeBuildsPath
	for _, suffix := range []string{"", "?", query + "&expected_role=", query + "&expected_role=bad", query + "&url=private", query + "&after=bad", query + "&diagnostics=none", query + "&expected_role=single-box-v1&diagnostics=http_cron_delivery", query + "&expected_role=single-box-v1&diagnostics=builder_health", query + "&expected_role=single-box-v2&diagnostics=builder_health,builder_health"} {
		if rec := e.Do(t, "GET", path+suffix, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid selection: %d %s", rec.Code, rec.Body)
		}
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()
	(*e.S()).WithStatusCache(srv.URL, "")
	rec := e.Do(t, "GET", path+query, nil, nil)
	var out api.ObsRuntimeBuilds
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !operatorobs.RuntimeBuildsValid(out) || out.Reason != "expected_role_not_selected" || calls.Load() != 0 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unselected read: %d %s", rec.Code, rec.Body)
	}
	store := *(*e.S()).Store()
	before, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, "GET", path+query+"&expected_role=single-box-v1", nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !operatorobs.RuntimeBuildsValid(out) || out.Counts.Missing != 4 || calls.Load() != 40 || strings.Contains(rec.Body.String(), srv.URL) {
		t.Fatalf("missing identity: %d %s", rec.Code, rec.Body)
	}
	after, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil || before != after {
		t.Fatal("identity read changed alert history")
	}
}

func TestObsRuntimeBuildsDiagnosticSelection(t *testing.T) {
	e := gapEnv(t)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer source.Close()
	(*e.S()).WithStatusCache(source.URL, "")
	query := "?release=" + strings.Repeat("a", 40) + "&after=" + time.Now().UTC().Add(-time.Minute).Format(time.RFC3339) + "&expected_role=single-box-v2&diagnostics=none"
	response := e.Do(t, "GET", operatorobs.RuntimeBuildsPath+query, nil, nil)
	var out operatorobs.RuntimeBuildsLoopResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || !operatorobs.RuntimeBuildsValid(out.ObsRuntimeBuilds) || !operatorobs.RuntimeDiagnosticsValid(out.ExpectedRole, out.Diagnostics) || len(out.Diagnostics.Selected) != 0 || out.LoopHealth == nil || out.BuilderHealth != nil || out.HTTPCronDelivery != nil || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid protected selected read: %d", response.Code)
	}
}

func TestObsRuntimeBuildsAuthorization(t *testing.T) {
	path := operatorobs.RuntimeBuildsPath
	for _, tc := range []struct {
		scopes []string
		email  string
	}{{api.ScopesReadSurface, "customer@faas.dev"}, {api.ScopesAdminOnly, "rogue@faas.dev"}} {
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
