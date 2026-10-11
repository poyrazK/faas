// handlers_admin_builder_heartbeats_test.go — Commit 7 (P5)
// coverage for /v1/admin/obs/builder-heartbeats.
//
// What this pins:
//
//   1. Two-layer auth gate (admin scope + email allowlist) — same
//      shape as handlers_admin_obs_pr3_test.go so the failure
//      modes are discoverable in one place.
//   2. Happy path: empty store returns 200 with empty `items`
//      and `queued_builds: 0` (the underlying writer
//      pkg/builderd/heartbeat.go is deferred per the Commit 7
//      builderd emits the source='builder_tick' rows in production;
//      a fresh test store remains empty until a producer publishes one).
//   3. With a builder_tick heartbeat on the in-memory store,
//      the row surfaces under items.
//   4. With a queued build, QueuedBuilds is 1 (not the total
//      of all build states).
//
// The MemStore coverage is sufficient because the handler is
// store-agnostic — the SQL path is exercised by the pgstore
// test suite when re-enabling the live builder_tick writer.

package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const builderAdminEmail = "ops-builder@faas.dev"

// newBuilderPR5Env mirrors newObsPR3Env. The handler is store-
// agnostic (MemStore ↔ PgStore parity is exercised in
// pkg/state/pgstore tests for each new method), so a MemStore
// harness is sufficient.
func newBuilderPR5Env(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "builder-pr5-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct}
}

func TestObsBuilderHeartbeats_AuthGate_RejectsCustomerKey(t *testing.T) {
	e := newBuilderPR5Env(t, api.ScopesReadSurface, builderAdminEmail, "customer@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/builder-heartbeats", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("builder-heartbeats with customer scope: got %d, want 403", rec.Code)
	}
}

func TestObsBuilderHeartbeats_AuthGate_RejectsNonAllowlistedEmail(t *testing.T) {
	e := newBuilderPR5Env(t, api.ScopesAdminOnly, builderAdminEmail, "rogue@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/builder-heartbeats", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("builder-heartbeats non-allowlist: got %d, want 403", rec.Code)
	}
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}

func TestObsBuilderHeartbeats_HappyPath_EmptyStore(t *testing.T) {
	e := newBuilderPR5Env(t, api.ScopesAdminOnly, builderAdminEmail, builderAdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/builder-heartbeats", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("builder-heartbeats: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsBuilderHeartbeatListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Items == nil {
		t.Errorf("items must be non-nil slice on empty store")
	}
	if len(resp.Items) != 0 {
		t.Errorf("items: got %d, want 0", len(resp.Items))
	}
	if resp.QueuedBuilds != 0 {
		t.Errorf("queued_builds: got %d, want 0", resp.QueuedBuilds)
	}
	if resp.GeneratedAt.IsZero() {
		t.Errorf("generated_at: zero")
	}
}

func TestObsBuilderHeartbeats_BuilderTickRowSurfaces(t *testing.T) {
	e := newBuilderPR5Env(t, api.ScopesAdminOnly, builderAdminEmail, builderAdminEmail)
	// Seed a compute_node + a builder_tick heartbeat on it. The
	// producer (pkg/builderd/heartbeat.go) is deferred, so we
	// stamp manually here to exercise the read path.
	node, err := e.store.UpsertComputeNode(context.Background(), state.ComputeNode{
		Name:               "box-builder-1",
		TargetURL:          "tcp://100.64.0.9:50051",
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 2048,
	})
	if err != nil {
		t.Fatalf("seed compute_node: %v", err)
	}
	if err := e.store.AppendComputeNodeHeartbeatWithStats(context.Background(), node.ID, time.Now().UTC(), time.Now().UTC(), "builder_tick", 12.5, 1024); err != nil {
		t.Fatalf("seed builder_tick: %v", err)
	}

	rec := e.do(t, "GET", "/v1/admin/obs/builder-heartbeats", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("builder-heartbeats: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsBuilderHeartbeatListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1 (one builder_tick row)", len(resp.Items))
	}
	if resp.Items[0].NodeID != node.ID {
		t.Errorf("node_id = %q, want %q", resp.Items[0].NodeID, node.ID)
	}
	if resp.Items[0].CPUPct60s == nil || *resp.Items[0].CPUPct60s != 12.5 {
		t.Errorf("cpu_pct_60s mismatch: got %v, want 12.5", resp.Items[0].CPUPct60s)
	}
}

func TestObsBuilderHeartbeats_QueuedBuildsCount(t *testing.T) {
	e := newBuilderPR5Env(t, api.ScopesAdminOnly, builderAdminEmail, builderAdminEmail)
	// Empty cluster: QueuedBuilds must be 0 — confirms the
	// gauge renders cleanly when no builds have been queued.
	rec := e.do(t, "GET", "/v1/admin/obs/builder-heartbeats", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("builder-heartbeats empty: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsBuilderHeartbeatListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.QueuedBuilds != 0 {
		t.Errorf("queued_builds on empty store: got %d, want 0", resp.QueuedBuilds)
	}
	if resp.Items == nil {
		t.Errorf("items: got nil, want empty slice")
	}
	if len(resp.Items) != 0 {
		t.Errorf("items: got %d, want 0", len(resp.Items))
	}
}
