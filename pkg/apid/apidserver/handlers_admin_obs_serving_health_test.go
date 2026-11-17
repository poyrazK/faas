package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestObsServingHealthReadOnlyAndQueryValidation(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.ServingHealthPath
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
	var out api.ObsServingHealth
	rec := e.Do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "partial" || out.Reason != "prometheus_not_configured" || out.Probes == nil || out.Counts != nil {
		t.Fatalf("unconfigured serving read: %d %s", rec.Code, rec.Body)
	}
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "private upstream credentials", 503)
	}))
	defer srv.Close()
	(*e.S()).WithStatusCache(srv.URL, "")
	for _, query := range []string{"?", "?url=https://private.example/token", "?node=fsn-2", "?since=now"} {
		if rec := e.Do(t, "GET", path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("query accepted: %d %s", rec.Code, rec.Body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid query reached Prometheus")
	}
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "partial" || out.Counts != nil || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), srv.URL) {
		t.Fatalf("failed serving source: %d %s", rec.Code, rec.Body)
	}
	after, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil || before != after || !reflect.DeepEqual(prior, current) {
		t.Fatal("serving read changed retained history")
	}
}

func TestObsServingHealthAuthorization(t *testing.T) {
	path := operatorobs.ServingHealthPath
	for _, tc := range []struct {
		scopes []string
		email  string
	}{{api.ScopesReadSurface, "customer@faas.dev"}, {api.ScopesAdminOnly, "rogue@faas.dev"}} {
		e := apidserver.NewObsEnv(t, tc.scopes, "ops@faas.dev", tc.email)
		if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 403 {
			t.Fatalf("serving authorization: %d %s", rec.Code, rec.Body)
		}
	}
	h, account, manager, sessionID := apidserver.SetupMW(t, api.PlanPro, true)
	pending := apidserver.ReissueWithMFAFlag(t, manager, sessionID, account.ID, true)
	rec := apidserver.CookieDo(t, h, pending, http.MethodGet, path, nil)
	var problem api.Problem
	if rec.Code != 403 || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("serving MFA: %d %s", rec.Code, rec.Body)
	}
}
