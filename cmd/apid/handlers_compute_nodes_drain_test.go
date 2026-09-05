// handlers_compute_nodes_drain_test.go — coverage for
// POST/GET /v1/compute-nodes/{name}/drain (Workstream B / issue
// #1184 / ADR-137). Pin:
//   - POST happy path: lifecycle 'active' → 'draining' + 200
//     with the canonical payload.
//   - POST conflict: lifecycle 'draining' → 409 CodeNodeDraining.
//   - POST conflict: lifecycle 'recovering' → 409
//     CodeNodeRecoveryInProgress.
//   - POST bad transition: lifecycle 'unavailable' → 422
//     CodeNodeLifecycleInvalid.
//   - GET 404 when the node was never drained.
//   - GET happy path: drain progress + drained_instance_count.
//
// The recovery-event fan-out is exercised in
// pkg/events/recovery_test.go (Task #54); here we verify the
// handler tolerates a nil events.Platform and the CAS lands
// in the store.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// drainTestServer is a minimal server wired for the drain
// handler surface. eventsPlatform is nil — the handler tolerates
// nil and the fan-out is covered separately in pkg/events tests.
func drainTestServer(t *testing.T) (*server, *state.MemStore) {
	t.Helper()
	store := state.NewMemStore()
	srv := &server{
		store:          store,
		eventsPlatform: nil,
	}
	return srv, store
}

// seedActiveNode inserts a healthy node so the drain handler has
// something to flip.
func seedActiveNode(t *testing.T, store *state.MemStore, name string) state.ComputeNode {
	t.Helper()
	n, err := store.UpsertComputeNode(context.Background(), state.ComputeNode{
		Name: name, TargetURL: "unix:///run/faas/" + name + ".sock",
		VPCPUs: 160, MemMB: 56000, MaxConcurrency: 200,
		AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: api.VCPUSlots,
		Lifecycle: state.NodeLifecycleActive, Active: true,
	})
	if err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	return n
}

// TestPostComputeNodeDrain_HappyPath — POST flips the node to
// 'draining', returns 200 with the canonical payload. The POST
// handler re-reads after the CAS so the response includes
// drain_initiated_at.
func TestPostComputeNodeDrain_HappyPath(t *testing.T) {
	srv, store := drainTestServer(t)
	node := seedActiveNode(t, store, "node-a")

	req := httptest.NewRequest(http.MethodPost, "/v1/compute-nodes/node-a/drain", nil)
	req.SetPathValue("name", "node-a")
	rr := httptest.NewRecorder()
	srv.postComputeNodeDrain(rr, req, state.Account{})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Drain-Status"); got != "ok" {
		t.Errorf("X-Drain-Status = %q, want ok", got)
	}
	var resp drainResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if resp.Lifecycle != "draining" {
		t.Errorf("Lifecycle = %q, want draining", resp.Lifecycle)
	}
	if resp.NodeName != "node-a" {
		t.Errorf("NodeName = %q, want node-a", resp.NodeName)
	}
	if resp.DrainInitiatedAt == nil {
		t.Errorf("DrainInitiatedAt is nil; want a timestamp")
	}
	post, _ := store.ComputeNodeByID(context.Background(), node.ID)
	if post.Lifecycle != state.NodeLifecycleDraining {
		t.Errorf("store.Lifecycle = %q, want draining", post.Lifecycle)
	}
}

// TestPostComputeNodeDrain_AlreadyDraining — POST on a node
// already in 'draining' returns 409 CodeNodeDraining.
func TestPostComputeNodeDrain_AlreadyDraining(t *testing.T) {
	srv, store := drainTestServer(t)
	_ = seedActiveNode(t, store, "node-b")
	if err := store.NodeSetLifecycle(context.Background(), mustNodeID(t, store, "node-b"), state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatalf("pre-flip: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/compute-nodes/node-b/drain", nil)
	req.SetPathValue("name", "node-b")
	rr := httptest.NewRecorder()
	srv.postComputeNodeDrain(rr, req, state.Account{})

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), api.CodeNodeDraining) {
		t.Errorf("body missing %q; got %s", api.CodeNodeDraining, rr.Body.String())
	}
}

// TestPostComputeNodeDrain_Recovering — POST on a node in
// 'recovering' returns 409 CodeNodeRecoveryInProgress.
func TestPostComputeNodeDrain_Recovering(t *testing.T) {
	srv, store := drainTestServer(t)
	_ = seedActiveNode(t, store, "node-c")
	if err := store.NodeSetLifecycle(context.Background(), mustNodeID(t, store, "node-c"), state.NodeLifecycleActive, state.NodeLifecycleRecovering); err != nil {
		t.Fatalf("pre-flip: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/compute-nodes/node-c/drain", nil)
	req.SetPathValue("name", "node-c")
	rr := httptest.NewRecorder()
	srv.postComputeNodeDrain(rr, req, state.Account{})

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), api.CodeNodeRecoveryInProgress) {
		t.Errorf("body missing %q; got %s", api.CodeNodeRecoveryInProgress, rr.Body.String())
	}
}

// TestPostComputeNodeDrain_InvalidTransition — POST on a node
// in 'unavailable' returns 422 CodeNodeLifecycleInvalid.
func TestPostComputeNodeDrain_InvalidTransition(t *testing.T) {
	srv, store := drainTestServer(t)
	_ = seedActiveNode(t, store, "node-d")
	if err := store.NodeSetLifecycle(context.Background(), mustNodeID(t, store, "node-d"), state.NodeLifecycleActive, state.NodeLifecycleUnavailable); err != nil {
		t.Fatalf("pre-flip: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/compute-nodes/node-d/drain", nil)
	req.SetPathValue("name", "node-d")
	rr := httptest.NewRecorder()
	srv.postComputeNodeDrain(rr, req, state.Account{})

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), api.CodeNodeLifecycleInvalid) {
		t.Errorf("body missing %q; got %s", api.CodeNodeLifecycleInvalid, rr.Body.String())
	}
}

// TestPostComputeNodeDrain_NotFound — POST on a missing node
// returns 404 with the standard not-found problem shape.
func TestPostComputeNodeDrain_NotFound(t *testing.T) {
	srv, _ := drainTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/compute-nodes/nope/drain", nil)
	req.SetPathValue("name", "nope")
	rr := httptest.NewRecorder()
	srv.postComputeNodeDrain(rr, req, state.Account{})

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// TestGetComputeNodeDrainProgress_404 — GET on a node that was
// never drained returns 404.
func TestGetComputeNodeDrainProgress_404(t *testing.T) {
	srv, store := drainTestServer(t)
	_ = seedActiveNode(t, store, "node-e")

	req := httptest.NewRequest(http.MethodGet, "/v1/compute-nodes/node-e/drain", nil)
	req.SetPathValue("name", "node-e")
	rr := httptest.NewRecorder()
	srv.getComputeNodeDrainProgress(rr, req, state.Account{})

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// TestGetComputeNodeDrainProgress_OK — GET on a draining node
// returns 200 with drain_initiated_at + the lifecycle.
func TestGetComputeNodeDrainProgress_OK(t *testing.T) {
	srv, store := drainTestServer(t)
	_ = seedActiveNode(t, store, "node-f")
	id := mustNodeID(t, store, "node-f")
	if err := store.NodeSetLifecycle(context.Background(), id, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatalf("pre-flip: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/compute-nodes/node-f/drain", nil)
	req.SetPathValue("name", "node-f")
	rr := httptest.NewRecorder()
	srv.getComputeNodeDrainProgress(rr, req, state.Account{})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp drainResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Lifecycle != "draining" {
		t.Errorf("Lifecycle = %q, want draining", resp.Lifecycle)
	}
	if resp.DrainInitiatedAt == nil {
		t.Errorf("DrainInitiatedAt is nil; want a timestamp")
	}
}

// TestWriteDrainJSON_HeaderSet — writeDrainJSON always sets the
// X-Drain-Status header so the operator UI can render the right
// badge without parsing the body.
func TestWriteDrainJSON_HeaderSet(t *testing.T) {
	rr := httptest.NewRecorder()
	writeDrainJSON(rr, http.StatusOK, state.ComputeNode{Name: "node-x", Lifecycle: state.NodeLifecycleDraining}, 0, "ok")
	if got := rr.Header().Get("X-Drain-Status"); got != "ok" {
		t.Errorf("X-Drain-Status = %q, want ok", got)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"lifecycle":"draining"`)) {
		t.Errorf("body missing draining lifecycle; got %s", rr.Body.String())
	}
}

// mustNodeID is a tiny helper: tests above call it to look up
// the seeded node's ID. The store doesn't expose name → id
// outside ComputeNodeByName, so we round-trip through that.
func mustNodeID(t *testing.T, store *state.MemStore, name string) string {
	t.Helper()
	n, err := store.ComputeNodeByName(context.Background(), name)
	if err != nil {
		t.Fatalf("ComputeNodeByName(%q): %v", name, err)
	}
	return n.ID
}
