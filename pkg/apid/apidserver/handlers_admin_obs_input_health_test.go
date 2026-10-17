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

func TestObsInputHealthSelectionAndReadOnlyHistory(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.InputHealthPath
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
	var out api.ObsInputHealth
	rec := e.Do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "unknown" || out.Reason != "expected_role_not_selected" || len(out.AvailableRoles) != 2 || len(out.Jobs) != 0 {
		t.Fatalf("unselected: %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"?", "?expected_role=", "?expected_role=compute", "?expected_role=single-box-v1&expected_role=control-plane-v1", "?expected_role=control-plane-v1&url=private", "?limit=1"} {
		if rec := e.Do(t, "GET", path+q, nil, nil); rec.Code != 400 {
			t.Fatalf("query accepted: %s %d", q, rec.Code)
		}
	}
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()
	(*e.S()).WithStatusCache(srv.URL, "")
	if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 200 || calls.Load() != 0 {
		t.Fatal("unselected source read")
	}
	rec = e.Do(t, "GET", path+"?expected_role=single-box-v1", nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.State != "degraded" || len(out.Jobs) != 4 || *out.Jobs[0].Targets.Counts.MissingMinimum != 1 {
		t.Fatalf("missing targets: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), srv.URL) {
		t.Fatal("source URL exposed")
	}
	after, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil || before != after || !reflect.DeepEqual(prior, current) {
		t.Fatal("diagnostic read changed history")
	}
}

func TestObsInputHealthAuthorization(t *testing.T) {
	path := operatorobs.InputHealthPath
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
