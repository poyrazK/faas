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

const nodeProbeNotificationsPath = "/v1/admin/obs/nodes/fsn-2/probe-notifications"

func TestObsNodeProbeNotificationsReadOnlyAndSelection(t *testing.T) {
	e := gapEnv(t)
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
	rec := e.Do(t, http.MethodGet, nodeProbeNotificationsPath, nil, nil)
	var out api.ObsNodeProbeNotifications
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !operatorobs.NodeProbeNotificationsValid(out) || out.Expected != nil || out.Reason != "prometheus_not_configured" {
		t.Fatalf("unconfigured node read: %d %s", rec.Code, rec.Body)
	}
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "private-upstream-credentials", 503)
	}))
	t.Cleanup(srv.Close)
	(*e.S()).WithStatusCache(srv.URL, "")
	for _, path := range []string{nodeProbeNotificationsPath + "?", nodeProbeNotificationsPath + "?node=fsn-3", nodeProbeNotificationsPath + "?url=https://private/token", "/v1/admin/obs/nodes/bad%20node/probe-notifications", "/v1/admin/obs/nodes/a..b/probe-notifications"} {
		if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid selection accepted: %d %s", rec.Code, rec.Body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid selection reached Prometheus")
	}
	rec = e.Do(t, http.MethodGet, nodeProbeNotificationsPath, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !operatorobs.NodeProbeNotificationsValid(out) || out.Expected != nil || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), srv.URL) {
		t.Fatalf("failed node source leaked or became known: %d %s", rec.Code, rec.Body)
	}
	after, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil || before != after || !reflect.DeepEqual(prior, current) {
		t.Fatal("node read changed retained monitoring history")
	}
}

func TestObsNodeProbeNotificationsAuthorization(t *testing.T) {
	for _, tc := range []struct {
		scopes []string
		email  string
	}{
		{api.ScopesReadSurface, "customer@faas.dev"}, {api.ScopesAdminOnly, "rogue@faas.dev"},
	} {
		e := apidserver.NewObsEnv(t, tc.scopes, "ops@faas.dev", tc.email)
		var calls atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); http.Error(w, "unreachable", 503) }))
		(*e.S()).WithStatusCache(srv.URL, "")
		if rec := e.Do(t, http.MethodGet, nodeProbeNotificationsPath, nil, nil); rec.Code != 403 {
			t.Fatalf("unauthorized node read: %d %s", rec.Code, rec.Body)
		}
		if calls.Load() != 0 {
			t.Fatal("unauthorized read reached source")
		}
		srv.Close()
	}
	h, account, manager, sessionID := apidserver.SetupMW(t, api.PlanPro, true)
	pending := apidserver.ReissueWithMFAFlag(t, manager, sessionID, account.ID, true)
	rec := apidserver.CookieDo(t, h, pending, http.MethodGet, nodeProbeNotificationsPath, nil)
	var problem api.Problem
	if rec.Code != 403 || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("node lookup bypassed MFA: %d %s", rec.Code, rec.Body)
	}
}
