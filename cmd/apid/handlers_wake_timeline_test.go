// handlers_wake_timeline_test.go — issue #517 / PR-C / ADR-064 —
// whitebox for GET /v1/apps/{slug}/wakes/{wake_id}/timeline.
//
// The test pins four invariants:
//
//   - Oldest-first ordering (the customer-facing timeline
//     reads as a forward narrative; the partial index
//     events_wake_id_idx orders by at ASC).
//   - Forge-proof: a row whose data.app_id does not match the
//     resolved app is dropped silently (cross-account
//     invisibility).
//   - Unknown wake_id 404s the same way an unknown slug does
//     (no enumeration via ID-probing).
//   - The ?since query param floors the read at a given
//     timestamp (used by the dashboard's "load older"
//     infinite-scroll).
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestListWakeTimeline_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "tl-app-1")
	platform := events.NewPlatform("test", e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), wire.NewOpsMetrics("apid-tl-test"), nil)
	e.s.WithEventsPlatform(platform)

	// Three frames in oldest-first order. MemStore.AppendEvent
	// stamps time.Now() at the call moment (not the typed
	// EmitAt — see pkg/state/memstore.go:~4345), so sleep between
	// calls to guarantee monotonic at-stamps. The 5ms gap is well
	// above the memstore's per-call resolution (~1µs on a
	// developer Mac) so the order is stable.
	wakeID := "wake-tl-1"
	ctx := context.Background()
	platform.Emit(ctx, events.QueueAccepted{
		EmitAt: time.Now().UTC(),
		WakeID: wakeID, AppID: app.ID, RequestID: "r-1",
	})
	time.Sleep(5 * time.Millisecond)
	platform.Emit(ctx, events.Admitted{
		EmitAt: time.Now().UTC(),
		WakeID: wakeID, AppID: app.ID, RequestID: "r-1", AccountID: e.acct.ID, Plan: string(e.acct.Plan),
	})
	time.Sleep(5 * time.Millisecond)
	platform.Emit(ctx, events.BootStarted{
		EmitAt: time.Now().UTC(),
		WakeID: wakeID, AppID: app.ID, InstanceID: "inst-1", NodeID: "node-1", Method: "restore",
	})

	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/wakes/"+wakeID+"/timeline", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.WakeTimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WakeID != wakeID {
		t.Errorf("wake_id = %q, want %q", resp.WakeID, wakeID)
	}
	if resp.AppID != app.ID {
		t.Errorf("app_id = %q, want %q", resp.AppID, app.ID)
	}
	if len(resp.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(resp.Events))
	}
	wantKinds := []string{"wake.queue_accepted", "wake.admitted", "wake.boot_started"}
	for i, e := range resp.Events {
		if e.Kind != wantKinds[i] {
			t.Errorf("event[%d].kind = %q, want %q (must be ordered at ASC)", i, e.Kind, wantKinds[i])
		}
	}
	if resp.Events[0].At >= resp.Events[1].At {
		t.Errorf("at ordering broken: %s !< %s", resp.Events[0].At, resp.Events[1].At)
	}
}

func TestListWakeTimeline_UnknownWakeIDIs404(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "tl-app-2")
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/wakes/wake-unknown/timeline", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestListWakeTimeline_DropsCrossAccountRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "tl-app-3")
	// Plant a row whose data.app_id is some other app's id
	// (the forge attempt). The handler MUST drop it (no
	// cross-account leakage in the response).
	platform := events.NewPlatform("test", e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), wire.NewOpsMetrics("apid-tl-test"), nil)
	platform.Emit(context.Background(), events.BootStarted{
		EmitAt:     time.Now().UTC(),
		WakeID:     "wake-tl-3",
		AppID:      "00000000-0000-0000-0000-000000000000", // not this app
		InstanceID: "inst-forge",
		NodeID:     "node-1",
		Method:     "restore",
	})
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/wakes/wake-tl-3/timeline", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.WakeTimelineResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Events) != 0 {
		t.Errorf("forge-proof: got %d events, want 0 (cross-account row leaked)", len(resp.Events))
	}
}

func TestListWakeTimeline_SinceFiltersOlderRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "tl-app-4")
	platform := events.NewPlatform("test", e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), wire.NewOpsMetrics("apid-tl-test"), nil)
	wakeID := "wake-tl-4"
	platform.Emit(context.Background(), events.QueueAccepted{
		EmitAt: time.Now().UTC(), WakeID: wakeID, AppID: app.ID, RequestID: "r-old",
	})
	// Sleep 50ms so the second row's stamped `At` is strictly
	// after the first (MemStore.AppendEvent stamps time.Now(),
	// not the typed EmitAt — see pkg/state/memstore.go:4345).
	time.Sleep(50 * time.Millisecond)
	midpoint := time.Now().UTC()
	time.Sleep(50 * time.Millisecond)
	platform.Emit(context.Background(), events.BootCompleted{
		EmitAt: time.Now().UTC(), WakeID: wakeID, AppID: app.ID, InstanceID: "inst-1", NodeID: "node-1", Method: "restore",
	})
	// Use the midpoint as the since floor — only the fresh row survives.
	since := midpoint.Format(time.RFC3339Nano)
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/wakes/"+wakeID+"/timeline?since="+since, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.WakeTimelineResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1 (since must floor)", len(resp.Events))
	}
	if resp.Events[0].Kind != "wake.boot_completed" {
		t.Errorf("event.kind = %q, want wake.boot_completed", resp.Events[0].Kind)
	}
}

func TestListWakeTimeline_DeduplicatesVMMDMirror(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "tl-app-mirror")
	wakeID := "wake-tl-mirror"
	canonical := []byte(`{"wake_id":"wake-tl-mirror","app_id":"` + app.ID + `","instance_id":"inst-1","node_id":"node-1","trigger":"gateway","at_capacity":true}`)
	mirror := []byte(`{"wake_id":"wake-tl-mirror","app_id":"` + app.ID + `","instance_id":"inst-1","trigger":"gateway","at_capacity":false}`)
	if err := e.store.AppendEvent(context.Background(), "schedd", "wake.boot_started", nil, canonical); err != nil {
		t.Fatalf("append canonical: %v", err)
	}
	if err := e.store.AppendEvent(context.Background(), "vmmd", "wake.boot_started", nil, mirror); err != nil {
		t.Fatalf("append mirror: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/wakes/"+wakeID+"/timeline", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.WakeTimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want one canonical boot event", len(resp.Events))
	}
	if resp.Events[0].Actor != "schedd" {
		t.Fatalf("actor = %q, want schedd canonical event", resp.Events[0].Actor)
	}
	if got, ok := resp.Events[0].Data["at_capacity"].(bool); !ok || !got {
		t.Fatalf("at_capacity = %#v, want canonical true", resp.Events[0].Data["at_capacity"])
	}
}

// TestListWakeTimeline_LimitAboveMaxIs400 pins the explicit
// early-return on overflow (the CodeQL-blessed sanitizer for
// go/uncontrolled-allocation-size, CWE-770). The previous shape
// silently clamped `limit` to Max; the new shape returns 400 so
// `limit` is bounded at the parse site by an unconditional
// upper-bound check that the static analyzer can see — closing
// the taint path that previously reached the make() at the
// render site.
func TestListWakeTimeline_LimitAboveMaxIs400(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "tl-app-5")
	rec := e.do(t, http.MethodGet,
		"/v1/apps/"+app.Slug+"/wakes/wake-tl-5/timeline?limit=2000", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s, want 400 (limit > Max must reject, not clamp)", rec.Code, rec.Body.String())
	}
}

// seedAppForTimeline is the testEnv-side helper. Reuses the
// audit test's seedAppForAudit shape (POST /v1/apps + decode
// the AppResponse) so the wake-timeline tests don't fork the
// pattern.
func seedAppForTimeline(t *testing.T, e testEnv, slug string) api.AppResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(api.CreateAppRequest{Slug: slug})
	req := httptest.NewRequest(http.MethodPost, "/v1/apps", ioReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.key)
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed app %q: code=%d body=%s", slug, rec.Code, rec.Body.String())
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode seed app: %v", err)
	}
	return out
}

// ioReader is a tiny shim so the seedAppForTimeline test can
// pre-encode the body without pulling in bytes.Buffer.
type bytesReaderImpl struct {
	b []byte
	i int
}

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func ioReader(b []byte) io.Reader { return &bytesReaderImpl{b: b} }
