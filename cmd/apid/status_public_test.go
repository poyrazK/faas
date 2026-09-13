package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublicStatusOverviewCombinesTelemetryHistoryAndPublicEvents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := state.NewMemStore()
	incident, err := store.CreatePublicStatusEvent(t.Context(), state.StatusEventCreate{
		IdempotencyKey: "public-test-incident", Actor: "operator@example.com", Kind: publicstatus.KindIncident,
		Title: "API requests failing", Impact: publicstatus.StateMajorOutage,
		Components: []publicstatus.Component{publicstatus.ComponentAPIConsole},
		State:      publicstatus.LifecycleInvestigating, StartsAt: &now, Message: "We are investigating elevated errors.",
	})
	if err != nil {
		t.Fatal(err)
	}
	start, end := now.Add(24*time.Hour), now.Add(25*time.Hour)
	_, err = store.CreatePublicStatusEvent(t.Context(), state.StatusEventCreate{
		IdempotencyKey: "public-test-maint", Actor: "operator@example.com", Kind: publicstatus.KindMaintenance,
		Title: "Network maintenance", Impact: publicstatus.StateMaintenance,
		Components: []publicstatus.Component{publicstatus.ComponentNetworking}, State: publicstatus.LifecycleScheduled,
		ScheduledStartAt: &start, ScheduledEndAt: &end, Message: "Routine network maintenance.",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.CreatePublicStatusEvent(t.Context(), state.StatusEventCreate{
		IdempotencyKey: "public-test-resolved", Actor: "operator@example.com", Kind: publicstatus.KindIncident,
		Title: "Build delays", Impact: publicstatus.StateDegraded,
		Components: []publicstatus.Component{publicstatus.ComponentDeployments}, State: publicstatus.LifecycleIdentified,
		StartsAt: &now, Message: "Builds are delayed.",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendPublicStatusUpdate(t.Context(), resolved.PublicID, state.StatusEventUpdateInput{
		IdempotencyKey: "public-test-resolve", Actor: "operator@example.com", State: publicstatus.LifecycleResolved,
		Message: "Build throughput has recovered.", At: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"component":"builderd","severity":"page"},"value":[1700000000,"1"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"99.9"]}]}}`))
	}))
	t.Cleanup(prom.Close)
	srv := newServer(store, slog.Default(), "gregale.dev", nil).WithStatusCache(prom.URL, "")
	rec := httptest.NewRecorder()
	srv.publicStatusOverviewHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=15, stale-while-revalidate=45" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var body api.PublicStatusOverview
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OverallStatus != string(publicstatus.StateMajorOutage) || body.DataStatus != "fresh" {
		t.Fatalf("overview status = %q data=%q", body.OverallStatus, body.DataStatus)
	}
	if len(body.Components) != 5 {
		t.Fatalf("components = %d, want 5", len(body.Components))
	}
	for _, component := range body.Components {
		if len(component.Daily) != 30 {
			t.Fatalf("%s daily = %d, want 30", component.ID, len(component.Daily))
		}
	}
	if len(body.ActiveEvents) != 1 || body.ActiveEvents[0].ID != incident.PublicID || len(body.UpcomingMaintenance) != 1 || len(body.ResolvedIncidents) != 1 {
		t.Fatalf("event projection = active:%#v maintenance:%#v resolved:%#v", body.ActiveEvents, body.UpcomingMaintenance, body.ResolvedIncidents)
	}
	if strings.Contains(rec.Body.String(), "operator@example.com") || strings.Contains(rec.Body.String(), "builderd") {
		t.Fatalf("public response leaked operator or daemon detail: %s", rec.Body.String())
	}
}

func TestPublicStatusEndpointsKeepNonFinitePrometheusValuesOutOfJSON(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1700000000,"NaN"]}]}}`))
	}))
	t.Cleanup(prom.Close)
	srv := newServer(state.NewMemStore(), slog.Default(), "gregale.dev", nil).WithStatusCache(prom.URL, "")

	overviewRecorder := httptest.NewRecorder()
	srv.publicStatusOverviewHandler(overviewRecorder, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	var overview api.PublicStatusOverview
	if err := json.Unmarshal(overviewRecorder.Body.Bytes(), &overview); err != nil {
		t.Fatalf("overview emitted invalid JSON: %v; body=%q", err, overviewRecorder.Body.String())
	}
	for _, indicator := range overview.Indicators {
		if indicator.Value != nil {
			t.Fatalf("indicator %s value=%v, want unavailable null", indicator.ID, *indicator.Value)
		}
	}

	legacyRecorder := httptest.NewRecorder()
	srv.statusJSONHandler(legacyRecorder, httptest.NewRequest(http.MethodGet, "/status/slo.json", nil))
	var legacy StatusPage
	if err := json.Unmarshal(legacyRecorder.Body.Bytes(), &legacy); err != nil {
		t.Fatalf("legacy endpoint emitted invalid JSON: %v; body=%q", err, legacyRecorder.Body.String())
	}
}

func TestLegacyStatusProjectionIncludesOperatorIncidents(t *testing.T) {
	store := state.NewMemStore()
	now := time.Now().UTC()
	_, err := store.CreatePublicStatusEvent(t.Context(), state.StatusEventCreate{
		IdempotencyKey: "legacy-overlay", Actor: "operator@example.com", Kind: publicstatus.KindIncident,
		Title: "Declared outage", Impact: publicstatus.StateMajorOutage,
		Components: []publicstatus.Component{publicstatus.ComponentAPIConsole}, State: publicstatus.LifecycleInvestigating,
		StartsAt: &now, Message: "Investigating.",
	})
	if err != nil {
		t.Fatal(err)
	}
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"100"]}]}}`))
	}))
	t.Cleanup(prom.Close)
	srv := newServer(store, slog.Default(), "gregale.dev", nil).WithStatusCache(prom.URL, "")
	recorder := httptest.NewRecorder()
	srv.statusJSONHandler(recorder, httptest.NewRequest(http.MethodGet, "/status/slo.json", nil))
	var legacy StatusPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	if !legacy.Degraded || !strings.Contains(legacy.Source, "operator status event") {
		t.Fatalf("legacy projection = %+v, want operator outage reflected", legacy)
	}
}

type failingStatusEventStore struct{ state.Store }

func (f failingStatusEventStore) ListPublicStatusEvents(context.Context, state.StatusEventListOptions) ([]state.StatusIncident, error) {
	return nil, errors.New("status event database unavailable")
}

func TestPublicStatusOverviewUsesLastSnapshotWhenEventStoreFails(t *testing.T) {
	cache := newStatusCache("", slog.Default())
	cache.setPublic(api.PublicStatusOverview{OverallStatus: "degraded", DataStatus: "fresh", UpdatedAt: time.Now().Add(-time.Minute)}, time.Now().Add(-time.Minute))
	srv := newServer(failingStatusEventStore{Store: state.NewMemStore()}, slog.Default(), "gregale.dev", nil)
	srv.statusCache = cache
	recorder := httptest.NewRecorder()
	srv.publicStatusOverviewHandler(recorder, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var overview api.PublicStatusOverview
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.OverallStatus != "degraded" || overview.DataStatus != "stale" {
		t.Fatalf("fallback overview = %+v", overview)
	}
}

func TestFreshAlertTelemetryWritesRollupWhenIndicatorsAreStale(t *testing.T) {
	store := state.NewMemStore()
	srv := newServer(store, slog.Default(), "gregale.dev", nil).WithStatusCache("", "")
	srv.statusCache.cached = statusEvaluation{
		legacy: StatusPage{AsOf: time.Now().Add(-2 * time.Minute)}, states: publicstatus.Evaluate(nil, nil),
		indicatorAvailable: map[string]bool{}, dataStatus: "fresh", updatedAt: time.Now().Add(-2 * time.Minute), telemetryAvailable: true,
	}
	srv.statusCache.hasCached = true
	srv.statusCache.lastEval = time.Now()
	srv.runStatusEvaluationOnce(t.Context())
	buckets, err := store.ListStatusBuckets(t.Context(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != len(publicstatus.AllComponents()) {
		t.Fatalf("alert-backed evaluation persisted %d buckets, want %d", len(buckets), len(publicstatus.AllComponents()))
	}
	for _, bucket := range buckets {
		if !bucket.HasTelemetry {
			t.Fatalf("bucket for %s lost its alert telemetry marker", bucket.Component)
		}
	}
}

func TestColdCacheEventStoreFailureCannotReportOperational(t *testing.T) {
	cache := newStatusCache("", slog.Default())
	cache.cached = statusEvaluation{
		legacy: StatusPage{AsOf: time.Now()}, states: publicstatus.Evaluate(nil, nil),
		indicatorAvailable: map[string]bool{}, dataStatus: "fresh", updatedAt: time.Now(), telemetryAvailable: true,
	}
	cache.hasCached = true
	cache.lastEval = time.Now()
	srv := newServer(failingStatusEventStore{Store: state.NewMemStore()}, slog.Default(), "gregale.dev", nil)
	srv.statusCache = cache
	recorder := httptest.NewRecorder()
	srv.publicStatusOverviewHandler(recorder, httptest.NewRequest(http.MethodGet, "/v1/status", nil))

	var overview api.PublicStatusOverview
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.OverallStatus != string(publicstatus.StateUnknown) || overview.DataStatus != "unavailable" {
		t.Fatalf("overall=%q data=%q, want unknown/unavailable", overview.OverallStatus, overview.DataStatus)
	}
}

func TestPublicStatusIncidentHandlerUsesPublicUUIDAndNoStoreReturns404(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.Default(), "gregale.dev", nil).WithStatusCache("", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status/incidents/00000000-0000-0000-0000-000000000000", nil)
	req.SetPathValue("public_id", "00000000-0000-0000-0000-000000000000")
	srv.publicStatusIncidentHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPublicStatusOverviewNeverTranslatesMissingTelemetryToOperational(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.Default(), "gregale.dev", nil).WithStatusCache("", "")
	recorder := httptest.NewRecorder()
	srv.publicStatusOverviewHandler(recorder, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var overview api.PublicStatusOverview
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.OverallStatus != string(publicstatus.StateUnknown) || overview.DataStatus != "unavailable" {
		t.Fatalf("overall=%q data=%q, want unknown/unavailable", overview.OverallStatus, overview.DataStatus)
	}
	for _, component := range overview.Components {
		if component.Status != string(publicstatus.StateUnknown) {
			t.Fatalf("component %s status=%q, want unknown", component.ID, component.Status)
		}
	}
}

func TestPublicStatusIncidentHandlerRejectsMalformedUUID(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.Default(), "gregale.dev", nil).WithStatusCache("", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status/incidents/not-a-uuid", nil)
	req.SetPathValue("public_id", "not-a-uuid")
	srv.publicStatusIncidentHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestStatusEventOverlaysIncludesOnlyEventsAffectingCurrentState(t *testing.T) {
	now := time.Now().UTC()
	resolvedAt := now
	events := []state.StatusIncident{
		{Kind: publicstatus.KindIncident, Impact: publicstatus.StateMajorOutage, Components: []publicstatus.Component{publicstatus.ComponentAPIConsole}},
		{Kind: publicstatus.KindIncident, Impact: publicstatus.StateDegraded, Components: []publicstatus.Component{publicstatus.ComponentDeployments}, ResolvedAt: &resolvedAt},
		{Kind: publicstatus.KindMaintenance, State: publicstatus.LifecycleScheduled, Impact: publicstatus.StateMaintenance, Components: []publicstatus.Component{publicstatus.ComponentNetworking}},
		{Kind: publicstatus.KindMaintenance, State: publicstatus.LifecycleInProgress, Impact: publicstatus.StateMaintenance, Components: []publicstatus.Component{publicstatus.ComponentObservability}},
	}
	overlays := statusEventOverlays(events)
	if len(overlays) != 2 {
		t.Fatalf("overlays = %#v, want active incident and in-progress maintenance only", overlays)
	}
	if overlays[0].State != publicstatus.StateMajorOutage || overlays[1].Components[0] != publicstatus.ComponentObservability {
		t.Fatalf("unexpected overlays: %#v", overlays)
	}
}
