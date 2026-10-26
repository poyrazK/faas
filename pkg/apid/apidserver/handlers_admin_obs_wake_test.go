package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

const testObsWakeID = "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func TestObsWakeTimelineReturnsOneWake(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	app := uuid.New().String()
	for _, ev := range []struct{ kind, wake string }{
		{"wake.admitted", testObsWakeID},
		{"wake.boot_completed", testObsWakeID},
		{"wake.admitted", "0193a1b2-0000-7e5f-8a9b-0c1d2e3f4a5b"},
	} {
		if err := (*e.Store()).AppendEvent(ctx, "schedd", ev.kind, &app, []byte(`{"wake_id":"`+ev.wake+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/wakes/"+testObsWakeID, nil, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d cache=%q body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	var got api.ObsWakeTimeline
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WakeID != testObsWakeID || got.Subject != app || len(got.Events) != 2 || got.Events[0].Kind != "wake.admitted" {
		t.Fatalf("timeline = %+v", got)
	}
}

func TestObsWakeTimelineRejects(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/v1/admin/obs/wakes/short", http.StatusBadRequest},
		{"/v1/admin/obs/wakes/" + testObsWakeID + "?limit=5", http.StatusBadRequest},
		{"/v1/admin/obs/wakes/" + testObsWakeID, http.StatusNotFound},
	} {
		if rec := e.DoOperator(t, http.MethodGet, tc.path, nil, nil); rec.Code != tc.want {
			t.Errorf("%s status = %d, want %d body=%s", tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
	customer := apidserver.NewObsEnv(t, []string{api.ScopeAppsRead}, "ops@faas.dev", "ops@faas.dev")
	if rec := customer.Do(t, http.MethodGet, "/v1/admin/obs/wakes/"+testObsWakeID, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rec.Code)
	}
}
