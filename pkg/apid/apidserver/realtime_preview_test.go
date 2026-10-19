package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
)

// adr: 20261019102657 — realtime preview routes answer as unregistered ones
// until the account is in the rollout; only then does the daemon flag decide.
func TestRealtimePreviewRollout(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	endpoint := createRealtimeEndpointForTest(t, e)
	path := "/v1/apps/rt-actions/realtime/endpoints/" + endpoint + "/occupancy?channel=lobby"
	absent := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no such API route") {
			t.Fatalf("%s = %d %s, want the unregistered-route 404", name, rec.Code, rec.Body)
		}
	}

	// Nobody listed: hidden even before authentication.
	unauthenticated := httptest.NewRecorder()
	(*e.H()).ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path, nil))
	absent("unauthenticated, nobody listed", unauthenticated)
	absent("nobody listed", e.Do(t, http.MethodGet, path, nil, nil))

	other, err := managedrealtime.ParsePreviewRollout(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithRealtimePreview(other)
	absent("another account listed", e.Do(t, http.MethodGet, path, nil, nil))

	listed, err := managedrealtime.ParsePreviewRollout((*e.Acct()).ID)
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithRealtimePreview(listed)
	if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "channel occupancy preview unavailable") {
		t.Fatalf("listed, flag off = %d %s, want the feature's own 404", rec.Code, rec.Body)
	}
}
