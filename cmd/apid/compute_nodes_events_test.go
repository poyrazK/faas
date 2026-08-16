// CP-1: tests for GET /v1/compute-nodes/events.
//
// The handler is a thin SSE wrapper around db.Subscribe (one channel:
// compute_nodes_changed). The test seam is the package-internal
// recordingNotifier (defined in handlers_events_test.go) so the SSE
// body can be observed without a real Postgres connection.
//
// The shape mirrors TestEvents_FiltersByAccount: drive the handler
// through httptest.NewRecorder in-process (race-safe; the existing
// /v1/events test uses the same pattern) rather than spinning up a
// real socket — the SSE response is long-lived, and a real net.Conn
// interacts badly with -race.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// newComputeNodeEventsFixture wires a server with the
// recordingNotifier (subscription channel exposed via the package-
// internal publish helper) and returns the in-process handler +
// bearer token.
func newComputeNodeEventsFixture(t *testing.T, notif *recordingNotifier, allowlistEmail, accountEmail string) (http.Handler, string) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), accountEmail, api.PlanPro)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	srv := newServerWithDeps(store, nil, "gregale.dev", notif, "", nil, nil, nil, nil, 0, "")
	srv.WithAdminAllowlist(allowlistEmail)
	return srv.handler(), key
}

// TestComputeNodes_Events_StreamsAllFrames confirms the handler
// emits one SSE frame per `compute_nodes_changed` notification the
// recordingNotifier publishes. Three publishes → three frames on the
// wire. Admin allowlist hits; auth chain passes.
func TestComputeNodes_Events_StreamsAllFrames(t *testing.T) {
	notif := newRecordingNotifier()
	handler, tok := newComputeNodeEventsFixture(t, notif, "ops@example.com", "ops@example.com")

	res := make(chan string, 1)
	go func() {
		req := httptest.NewRequest("GET", "/v1/compute-nodes/events", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		res <- rec.Body.String()
	}()

	// Give the handler a beat to subscribe before publishing.
	time.Sleep(50 * time.Millisecond)
	notif.publish(db.NotifyComputeNodesChanged, `{"node_id":"box-1","op":"upsert"}`)
	notif.publish(db.NotifyComputeNodesChanged, `{"node_id":"box-2","op":"upsert"}`)
	notif.publish(db.NotifyComputeNodesChanged, `{"node_id":"box-3","op":"deactivate"}`)
	time.Sleep(50 * time.Millisecond)

	// Close the channel to unblock the handler's select (mirrors the
	// existing TestEvents_FiltersByAccount teardown).
	close(notif.out)
	body := <-res

	for _, want := range []string{"box-1", "box-2", "box-3"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q frame in body:\n%s", want, body)
		}
	}
	// The handler emits the channel name as the SSE event field —
	// confirm the wire shape matches the dashboard /v1/events style.
	if !strings.Contains(body, "event: compute_nodes_changed") &&
		!strings.Contains(body, "event: "+db.NotifyComputeNodesChanged) {
		t.Errorf("missing event: header for compute_nodes_changed\n%s", body)
	}
}

// TestComputeNodes_Events_RequiresAdmin confirms the handler is
// admin-only at the email-allowlist gate (a token with a valid admin
// scope but a non-matching email still 403s — adminAllows is enforced
// inside the handler body, not in middleware).
func TestComputeNodes_Events_RequiresAdmin(t *testing.T) {
	notif := newRecordingNotifier()
	handler, tok := newComputeNodeEventsFixture(t, notif, "different-ops@example.com", "ops@example.com")

	req := httptest.NewRequest("GET", "/v1/compute-nodes/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("admin miss: status=%d, want 403", rec.Code)
	}
}
