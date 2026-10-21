package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
	"github.com/onebox-faas/faas/pkg/apid/realtimepreview"
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

// adr: 20261019102657 — the routes server.go gates are exactly the ones the
// served spec hides, so neither list can drift from the other.
func TestRealtimePreviewRoutesMatchTheSpecList(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`mux\.Handle(?:Func)?\("([^"]+)",\s*s\.realtimePreviewRoute\(`)
	var gated []string
	for _, m := range pattern.FindAllSubmatch(source, -1) {
		gated = append(gated, string(m[1]))
	}
	want := realtimepreview.Routes()
	slices.Sort(gated)
	slices.Sort(want)
	if !slices.Equal(gated, want) {
		for _, route := range gated {
			if !slices.Contains(want, route) {
				t.Errorf("server.go gates %s, which realtimepreview does not list", route)
			}
		}
		for _, route := range want {
			if !slices.Contains(gated, route) {
				t.Errorf("realtimepreview lists %s, which server.go does not gate", route)
			}
		}
	}
}

// adr: 20261019102657 — outside the rollout a method probe, the served spec,
// the endpoint's preview fields and the webhook vocabulary reveal nothing;
// once every account sees the preview they all do.
func TestRealtimePreviewDiscovery(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	endpoint := createRealtimeEndpointForTest(t, e)
	serve := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	tickets := "/v1/apps/rt-actions/realtime/endpoints/" + endpoint + "/tickets"
	update := "/v1/apps/rt-actions/realtime/endpoints/" + endpoint
	const event = "realtime.schedule.published"

	if rec := serve(http.MethodPut, tickets); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no such API route") {
		t.Fatalf("method probe outside the rollout = %d %s, want the unregistered-route 404", rec.Code, rec.Body)
	}
	if spec := serve(http.MethodGet, "/v1/openapi.yaml").Body.String(); strings.Contains(spec, "/realtime/endpoints/{id}/tickets") || strings.Contains(spec, "channel_rules") {
		t.Fatal("served spec lists the realtime preview outside the rollout")
	}
	if rec := e.Do(t, http.MethodPatch, update, map[string]any{"slug": "chat"}, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `unknown field \"slug\"`) {
		t.Fatalf("slug outside the rollout = %d %s, want the unknown-field 400", rec.Code, rec.Body)
	}
	if slices.Contains((*e.S()).AppWebhookEventsForTest((*e.Acct()).ID), event) {
		t.Fatalf("webhook vocabulary offers %s outside the rollout", event)
	}

	(*e.S()).WithRealtimePreview(apidserver.RealtimePreviewEveryone())
	if rec := serve(http.MethodPut, tickets); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method probe once published = %d %s, want 405", rec.Code, rec.Body)
	}
	if spec := serve(http.MethodGet, "/v1/openapi.yaml").Body.String(); !strings.Contains(spec, "/realtime/endpoints/{id}/tickets") || !strings.Contains(spec, "channel_rules") {
		t.Fatal("served spec omits the realtime preview once every account sees it")
	}
	if rec := e.Do(t, http.MethodPatch, update, map[string]any{"slug": "chat"}, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"slug":"chat"`) {
		t.Fatalf("slug in the rollout = %d %s", rec.Code, rec.Body)
	}
	if !slices.Contains((*e.S()).AppWebhookEventsForTest((*e.Acct()).ID), event) {
		t.Fatalf("webhook vocabulary lacks %s in the rollout", event)
	}
}
