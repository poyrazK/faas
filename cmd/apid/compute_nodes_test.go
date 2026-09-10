// Tests for /v1/compute-nodes (issue #98 / ADR-028).
//
// Pins the four contracts that matter for this slice:
//
//   1. Empty allowlist  → every route 403 admin_required (no
//      implicit "any authenticated caller is admin" path).
//   2. Allowlist miss   → 403 admin_required (a customer-tier
//      bearer token can't reach the operator surface).
//   3. Allowlist hit    → GET / POST / DELETE return 2xx and the
//      row round-trips through the store. The hard-delete guard
//      refuses to drop the synthetic default-local row.
//   4. Auth gate precedes parse → a 401 on a malformed bearer does
//      NOT leak the body's parse-error side (so a brute-force can't
//      fish for handler bodies via crafted JSON).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func newComputeNodeTestServer(t *testing.T, adminCSV, email string) (*httptest.Server, string) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), email, api.PlanPro)
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
	srv := newServerWithDeps(store, nil, "gregale.dev", nil, "", nil, nil, nil, nil, 0, "")
	srv.WithAdminAllowlist(adminCSV)
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	return ts, key
}

func doJSON(t *testing.T, method, url, body, token string, ts *httptest.Server) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestComputeNodes_EmptyAllowlistDeniesAll(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "" /* no admins */, "ops@example.com")
	resp := doJSON(t, "GET", "/v1/compute-nodes", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("empty allowlist allowed GET: status=%d", resp.StatusCode)
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("empty allowlist: status=%d, want 4xx", resp.StatusCode)
	}
}

func TestComputeNodes_AllowlistMissDenies(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "different-operator@example.com", "ops@example.com")
	resp := doJSON(t, "GET", "/v1/compute-nodes", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("allowlist miss: status=%d, want 403", resp.StatusCode)
	}
}

func TestComputeNodes_AllowlistHitUpsertsAndLists(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")

	body := `{
		"name":"box-east-1",
		"target_url":"tcp://100.64.0.1:50051",
		"vpcpus":160,"mem_mb":56000,
		"max_concurrency":200,"admission_ceiling_mb":47600
	}`
	resp := doJSON(t, "POST", "/v1/compute-nodes", body, tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST upsert: status=%d", resp.StatusCode)
	}
	var posted computeNodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&posted); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if posted.Name != "box-east-1" || !posted.Active {
		t.Errorf("POST response: name=%q active=%v", posted.Name, posted.Active)
	}
	if posted.ID == "" {
		t.Errorf("POST response: id empty")
	}

	resp = doJSON(t, "GET", "/v1/compute-nodes", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list: status=%d", resp.StatusCode)
	}
	var listed []computeNodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	// MemStore seeds the synthetic default-local row by default,
	// so the list always includes it on top of what we upserted.
	// Filter to the row we just posted to pin the round-trip.
	found := false
	for _, n := range listed {
		if n.Name == "box-east-1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("posted row missing from list: %+v", listed)
	}
}

func TestComputeNodes_RejectsUnreachableGatewayMetricsTarget(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")

	for _, target := range []string{
		"tcp://127.0.0.1:8080",
		"tcp://localhost:8080",
		"tcp://0.0.0.0:8080",
		"tcp://[::]:8080",
	} {
		body := `{"name":"bad-metrics-target","target_url":"tcp://100.64.0.1:50051","gateway_target_url":"` + target + `","vpcpus":8,"mem_mb":8192,"max_concurrency":16,"admission_ceiling_mb":4096}`
		resp := doJSON(t, "POST", "/v1/compute-nodes", body, tok, ts)
		if resp.StatusCode != http.StatusBadRequest {
			resp.Body.Close()
			t.Errorf("target %q: status=%d, want 400", target, resp.StatusCode)
			continue
		}
		resp.Body.Close()
	}
}

func TestComputeNodes_HardDeleteRefusesDefaultLocal(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")
	resp := doJSON(t, "DELETE", "/v1/compute-nodes/default-local?hard=1", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("default-local hard-delete: status=%d, want 409", resp.StatusCode)
	}
}

func TestComputeNodes_SoftDeleteDeactivates(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")

	body := `{
		"name":"box-soft",
		"target_url":"tcp://100.64.0.2:50051",
		"vpcpus":8,"mem_mb":8192,
		"max_concurrency":16,"admission_ceiling_mb":4096
	}`
	resp := doJSON(t, "POST", "/v1/compute-nodes", body, tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed: %d", resp.StatusCode)
	}

	resp = doJSON(t, "DELETE", "/v1/compute-nodes/box-soft", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("soft delete: status=%d, want 200", resp.StatusCode)
	}
	var got computeNodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if got.Active {
		t.Errorf("soft delete: row still active")
	}
}

// seedHeartbeatTestNode is the shared seed for the heartbeats-history
// tests: an admin allowlist, an account, an API key, and a single
// compute_node named "box-hb" so a test can address it by name.
// Returns the test server, the bearer token, and the node name.
func seedHeartbeatTestNode(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")
	body := `{
		"name":"box-hb",
		"target_url":"tcp://100.64.0.3:50051",
		"vpcpus":8,"mem_mb":8192,
		"max_concurrency":16,"admission_ceiling_mb":4096
	}`
	resp := doJSON(t, "POST", "/v1/compute-nodes", body, tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed compute node: status=%d", resp.StatusCode)
	}
	return ts, tok, "box-hb"
}

// TestComputeNodes_Heartbeats_RoundTrip seeds a compute_node, appends
// one heartbeat row, and confirms the handler returns the documented
// wire shape — including the first-row baseline (no `missed`/`stale`
// flags on row 0). Seeding a single row exercises both the JSON
// field set and the no-previous-row summary.
func TestComputeNodes_Heartbeats_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	ts, tok, name := seedHeartbeatSingleRow(t, now)
	resp := doJSON(t, "GET", "/v1/compute-nodes/"+name+"/heartbeats", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET heartbeats: status=%d", resp.StatusCode)
	}
	var out computeNodeHeartbeatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != name {
		t.Errorf("name=%q, want %q", out.Name, name)
	}
	if out.NodeID == "" {
		t.Errorf("node_id empty")
	}
	if len(out.Heartbeats) != 1 {
		t.Fatalf("len(heartbeats)=%d, want 1", len(out.Heartbeats))
	}
	row := out.Heartbeats[0]
	if row.Source != "heartbeat_tick" {
		t.Errorf("source=%q, want heartbeat_tick", row.Source)
	}
	if row.GapToPreviousMS != 0 || row.Missed || row.Stale {
		t.Errorf("first-row baseline broken: %+v", row)
	}
}

// seedHeartbeatSingleRow builds a fresh test server with one
// compute_node and exactly one heartbeat row at `at`. Returns the
// server, token, and node name. The handler test reuses this seam
// because the existing newComputeNodeTestServer does not expose its
// store — seeding history rows requires direct store access.
func seedHeartbeatSingleRow(t *testing.T, at time.Time) (*httptest.Server, string, string) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "ops@example.com", api.PlanPro)
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
	node, err := store.UpsertComputeNode(context.Background(), state.ComputeNode{
		Name:               "box-hb",
		TargetURL:          "tcp://100.64.0.3:50051",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
	})
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := store.AppendComputeNodeHeartbeat(context.Background(), node.ID, at, at, "heartbeat_tick"); err != nil {
		t.Fatalf("seed heartbeat: %v", err)
	}
	srv := newServerWithDeps(store, nil, "gregale.dev", nil, "", nil, nil, nil, nil, 0, "")
	srv.WithAdminAllowlist("ops@example.com")
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	return ts, key, "box-hb"
}

// TestComputeNodes_Heartbeats_SinceFilter seeds three rows across a
// 60s window and asserts that ?since=t returns only the rows ≥ t.
// The default window is 30m, so omitting ?since= also returns them
// (covered here for completeness).
func TestComputeNodes_Heartbeats_SinceFilter(t *testing.T) {
	// Anchor the seeded rows in the recent past so the handler's
	// default 30m window AND a future-free ?since= both find them.
	base := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	store := state.NewMemStore()
	node, err := store.UpsertComputeNode(context.Background(), state.ComputeNode{
		Name:      "box-hb",
		TargetURL: "tcp://100.64.0.4:50051",
		VPCPUs:    8, MemMB: 8192, MaxConcurrency: 16, AdmissionCeilingMB: 4096,
	})
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// rows at base+0s, base+20s, base+40s (all in the past relative
	// to handler now). The cutoff sits between row 0 and row 1.
	for i, gap := range []time.Duration{0, 20 * time.Second, 40 * time.Second} {
		at := base.Add(gap)
		if err := store.AppendComputeNodeHeartbeat(context.Background(), node.ID, at, at, "heartbeat_tick"); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	srv := newServerWithDeps(store, nil, "gregale.dev", nil, "", nil, nil, nil, nil, 0, "")
	srv.WithAdminAllowlist("ops@example.com")
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	tok := mintAdminToken(t, store)

	// ?since= between row 0 and row 1 → 2 rows. Must be in the
	// past relative to the handler's `now` (RFC 7807 400 on a
	// future timestamp), so use base+10s.
	cutoff := base.Add(10 * time.Second).Format(time.RFC3339Nano)
	resp := doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats?since="+cutoff, "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status=%d", resp.StatusCode)
	}
	var out computeNodeHeartbeatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Heartbeats) != 2 {
		t.Errorf("since-filter len=%d, want 2", len(out.Heartbeats))
	}
	if out.Since == "" {
		t.Errorf("since omitted despite query param")
	}
}

// TestComputeNodes_Heartbeats_MissingNode confirms a name that
// doesn't resolve returns 404 with the documented "no such
// TestComputeNodes_Heartbeats_SinceClamped confirms the
// `since_clamped` wire field surfaces when the operator asks for
// an older window than the 24h hard cap (F4). The clamped `since`
// is still emitted; the flag tells the operator their query was
// narrowed so a "did the box flap at 14:32 last Tuesday?" query
// doesn't silently get a 24h response.
func TestComputeNodes_Heartbeats_SinceClamped(t *testing.T) {
	ts, tok, _ := seedHeartbeatTestNode(t)
	// 7 days ago — well past the 24h cap.
	wayBack := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
	resp := doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats?since="+wayBack, "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status=%d", resp.StatusCode)
	}
	var out computeNodeHeartbeatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.SinceClamped {
		t.Errorf("since_clamped = false, want true (asked for 7d ago, capped at 24h)")
	}
	if out.Since == "" {
		t.Errorf("since empty despite a (clamped) query param")
	}
}

// TestComputeNodes_Heartbeats_SinceNotClamped confirms the inverse:
// a recent timestamp does NOT set since_clamped, so the field stays
// in the default false position (omitempty on the wire).
func TestComputeNodes_Heartbeats_SinceNotClamped(t *testing.T) {
	ts, tok, _ := seedHeartbeatTestNode(t)
	recent := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339Nano)
	resp := doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats?since="+recent, "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status=%d", resp.StatusCode)
	}
	var out computeNodeHeartbeatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.SinceClamped {
		t.Errorf("since_clamped = true, want false (5m ago is within the 24h window)")
	}
	if out.Since == "" {
		t.Errorf("since empty despite a query param")
	}
}

// compute_node" problem detail (not a 500 — a typo from the
// operator must not look like a server bug).
func TestComputeNodes_Heartbeats_MissingNode(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "ops@example.com", "ops@example.com")
	resp := doJSON(t, "GET", "/v1/compute-nodes/no-such-node/heartbeats", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing node: status=%d, want 404", resp.StatusCode)
	}
}

// TestComputeNodes_Heartbeats_BadSince pins the 400 on a malformed
// RFC 3339 timestamp — operators rely on this to catch typos without
// scrolling the server log.
func TestComputeNodes_Heartbeats_BadSince(t *testing.T) {
	ts, tok, _ := seedHeartbeatTestNode(t)
	resp := doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats?since=not-rfc3339", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad since: status=%d, want 400", resp.StatusCode)
	}
}

// TestComputeNodes_Heartbeats_LimitClamps confirms ?limit=10000
// silently caps at heartbeatMaxLimit (2000), and ?limit=garbage
// falls back to the default (200). Both are documented behaviours;
// a typo shouldn't 400 the operator.
func TestComputeNodes_Heartbeats_LimitClamps(t *testing.T) {
	ts, tok, _ := seedHeartbeatTestNode(t)
	resp := doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats?limit=10000", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("limit=10000: status=%d, want 200", resp.StatusCode)
	}
	resp = doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats?limit=garbage", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("limit=garbage: status=%d, want 200", resp.StatusCode)
	}
}

// TestComputeNodes_Heartbeats_AdminOnlyDenies confirms the admin
// gate is enforced at the handler level — a non-admin token (allowlist
// populated with a different email) gets 403 even with a valid admin
// scope on the API key. The admin scope proves the operator surface
// is double-gated (MFA + scope AND email allowlist).
func TestComputeNodes_Heartbeats_AdminOnlyDenies(t *testing.T) {
	ts, tok := newComputeNodeTestServer(t, "different-ops@example.com", "ops@example.com")
	resp := doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("admin miss: status=%d, want 403", resp.StatusCode)
	}
}

// TestComputeNodes_Heartbeats_GapClassification seeds rows at
// 30s/60s/95s gaps and asserts the wire shape's missed/stale flags
// match the property test pinned in pkg/sched. The handler test is
// the end-to-end echo of the oracle the package test pins.
func TestComputeNodes_Heartbeats_GapClassification(t *testing.T) {
	// Anchor in the past so the default 30m window sees the rows.
	base := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	store := state.NewMemStore()
	node, err := store.UpsertComputeNode(context.Background(), state.ComputeNode{
		Name:      "box-hb",
		TargetURL: "tcp://100.64.0.5:50051",
		VPCPUs:    8, MemMB: 8192, MaxConcurrency: 16, AdmissionCeilingMB: 4096,
	})
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// gaps: 0 (baseline), 30 (== interval → Missed=true), 60 (> interval, ≤
	// staleness → Missed=true), 95 (> staleness → Missed+Stale=true).
	gaps := []time.Duration{0, 30 * time.Second, 60 * time.Second, 95 * time.Second}
	at := base
	for i, gap := range gaps {
		if i > 0 {
			at = at.Add(gap)
		}
		if err := store.AppendComputeNodeHeartbeat(context.Background(), node.ID, at, at, "heartbeat_tick"); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	srv := newServerWithDeps(store, nil, "gregale.dev", nil, "", nil, nil, nil, nil, 0, "")
	srv.WithAdminAllowlist("ops@example.com")
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	tok := mintAdminToken(t, store)

	resp := doJSON(t, "GET", "/v1/compute-nodes/box-hb/heartbeats", "", tok, ts)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status=%d", resp.StatusCode)
	}
	var out computeNodeHeartbeatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Heartbeats) != 4 {
		t.Fatalf("rows=%d, want 4", len(out.Heartbeats))
	}
	type want struct{ Missed, Stale bool }
	wantFlags := []want{
		{},                          // row 0: baseline
		{Missed: true},              // gap == interval
		{Missed: true},              // gap 60s
		{Missed: true, Stale: true}, // gap 95s
	}
	for i, row := range out.Heartbeats {
		if row.Missed != wantFlags[i].Missed || row.Stale != wantFlags[i].Stale {
			t.Errorf("row %d: got missed=%v stale=%v, want missed=%v stale=%v",
				i, row.Missed, row.Stale, wantFlags[i].Missed, wantFlags[i].Stale)
		}
	}
}

// mintAdminToken is a small helper used by the heartbeats tests that
// own their own store. It mirrors the body of newComputeNodeTestServer
// but skips the HTTP server: callers build a server with newServerWithDeps
// and need just the bearer token.
func mintAdminToken(t *testing.T, store state.Store) string {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), "ops@example.com", api.PlanPro)
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
	return key
}
