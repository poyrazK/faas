// handlers_compute_nodes_drain.go — POST /v1/compute-nodes/{name}/drain
// + GET progress. Workstream B (issue #1184, ADR-137) closes the
// "operator has to use the /admin/ops surface to drain a node"
// gap: the same apid admin-scoped route that already does
// CreateComputeNode + DELETE handles drain. The /admin/ops
// surface stays for the legacy operator tools; the
// /v1/compute-nodes/{name}/drain shape is the canonical
// customer-facing (admin-scoped) endpoint.
//
// Behaviour:
//
//   - POST /v1/compute-nodes/{name}/drain
//     CAS compute_nodes.lifecycle 'active' → 'draining'. The
//     recovery arbiter (Task #59) owns the actual instance
//     migration / recreation; the apid handler only flips the
//     lifecycle and emits the node.draining event. Returns 409
//     CodeNodeDraining if already draining, 409
//     CodeNodeRecoveryInProgress if recovering, 422
//     CodeNodeLifecycleInvalid otherwise.
//
//   - GET /v1/compute-nodes/{name}/drain
//     Returns drain status: {lifecycle, drain_initiated_at,
//     drain_completed_at, drained_instance_count}. A 404 if the
//     node is not (and never was) draining so the dashboard
//     can distinguish "not drained" from "drained and complete".
//
//   - ?wait=1 on POST: blocks until lifecycle is no longer
//     'draining' (i.e. drain completed OR failed). Caps at
//     drainWaitMaxPolls × drainWaitInterval = ~50s so a truly
//     stuck drain doesn't pin the request indefinitely. On
//     timeout, returns 200 with the current state so the
//     operator UI can render a spinner rather than an error.
//
// Why a separate handler file rather than extending
// handlers_admin_operator_ops.go: the apid
// /v1/compute-nodes/{name}/* surface is the customer-facing
// admin CRUD plane (mounts under ScopesAdminOnly + MFA); the
// /v1/admin/ops/* surface is the legacy operator-tools plane.
// Mixing them would force one route to live in two routers,
// which the §4 router invariants forbid.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// drainWaitInterval is the poll cadence for ?wait=1. 200ms keeps
// the blocking request off the hot path while still surfacing a
// completed drain within a human-perceptible window.
const drainWaitInterval = 200 * time.Millisecond

// drainWaitMaxPolls caps the ?wait=1 blocking duration. 250 ×
// 200ms = 50s — matches the wake-side timeout budget (placement
// refusal after 30s + buffer for the migration lease). Past 50s
// the operator UI should poll via GET rather than block.
const drainWaitMaxPolls = 250

// drainResponse is the JSON wire shape for both POST and GET.
// The shape is intentionally small: the operator UI needs the
// lifecycle + timestamps to render the badge, not the full
// ComputeNode.
type drainResponse struct {
	NodeName             string     `json:"node_name"`
	Lifecycle            string     `json:"lifecycle"`
	DrainInitiatedAt     *time.Time `json:"drain_initiated_at,omitempty"`
	DrainCompletedAt     *time.Time `json:"drain_completed_at,omitempty"`
	DrainedInstanceCount int        `json:"drained_instance_count"`
	LastRecoveryOutcome  *string    `json:"last_recovery_outcome,omitempty"`
}

// postComputeNodeDrain handles POST /v1/compute-nodes/{name}/drain.
// The handler CAS-transitions the node's lifecycle; it does NOT
// touch any instance row — the recovery arbiter (Task #59) owns
// the per-instance migration / recreation decision and emits the
// node.drained / instance.migrated / instance.recreated events
// when the sweep completes.
func (s *server) postComputeNodeDrain(w http.ResponseWriter, r *http.Request, _ state.Account) {
	nodeName := r.PathValue("name")
	if nodeName == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "missing node name", "path parameter {name} is required"))
		return
	}
	node, err := s.store.ComputeNodeByName(r.Context(), nodeName)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Node not found", err.Error()))
		return
	}
	switch node.Lifecycle {
	case state.NodeLifecycleDraining:
		api.WriteProblem(w, api.ErrNodeDraining(node.Name, string(node.Lifecycle)))
		return
	case state.NodeLifecycleUnavailable:
		api.WriteProblem(w, api.ErrNodeLifecycleInvalid(string(node.Lifecycle), string(state.NodeLifecycleDraining)))
		return
	case state.NodeLifecycleRecovering:
		api.WriteProblem(w, api.ErrNodeRecoveryInProgress(node.Name))
		return
	case state.NodeLifecycleActive, "":
		// Empty lifecycle is legacy pre-#1184 — treat as active.
	default:
		api.WriteProblem(w, api.ErrNodeLifecycleInvalid(string(node.Lifecycle), string(state.NodeLifecycleDraining)))
		return
	}
	expected := node.Lifecycle
	if expected == "" {
		expected = state.NodeLifecycleActive
	}
	if err := s.store.NodeSetLifecycle(r.Context(), node.ID, expected, state.NodeLifecycleDraining); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Node not found", err.Error()))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			// Race: heartbeat flipped to unavailable while we
			// were validating. Re-read + surface the precise
			// 409 / 422 code.
			fresh, ferr := s.store.ComputeNodeByName(r.Context(), nodeName)
			if ferr == nil {
				switch fresh.Lifecycle {
				case state.NodeLifecycleUnavailable:
					api.WriteProblem(w, api.ErrNodeLifecycleInvalid(string(fresh.Lifecycle), string(state.NodeLifecycleDraining)))
				case state.NodeLifecycleDraining:
					api.WriteProblem(w, api.ErrNodeDraining(node.Name, string(fresh.Lifecycle)))
				default:
					api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeCapacity, "drain CAS race", "please retry"))
				}
				return
			}
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeCapacity, "drain CAS race", "please retry"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeCapacity, "could not change compute-node lifecycle", err.Error()))
		return
	}
	if s.audit != nil {
		subject := node.ID
		s.audit.Emit(r.Context(), "operator.action.node_drain", &subject, map[string]any{
			"node_id":   node.ID,
			"node_name": node.Name,
		})
	}
	if s.eventsPlatform != nil {
		s.eventsPlatform.EmitRecovery(r.Context(), events.NodeDrainingEvent{
			EmitAt:      time.Now().UTC(),
			NodeID:      node.ID,
			NodeName:    node.Name,
			InitiatedAt: time.Now().UTC(),
		})
	}
	if r.URL.Query().Get("wait") == "1" {
		s.waitForDrainComplete(w, r, nodeName)
		return
	}
	fresh, ferr := s.store.ComputeNodeByName(r.Context(), nodeName)
	if ferr != nil {
		// Drain landed; follow-up read failed. Return 202
		// with a minimal payload so the operator UI can
		// re-fetch via GET.
		writeDrainJSON(w, http.StatusAccepted, state.ComputeNode{ID: node.ID, Name: node.Name, Lifecycle: state.NodeLifecycleDraining}, 0, "drain-accepted-followup-failed")
		return
	}
	writeDrainJSON(w, http.StatusOK, fresh, 0, "ok")
}

// getComputeNodeDrainProgress handles GET
// /v1/compute-nodes/{name}/drain. Returns 404 if the node is
// not (and never was) draining so the operator UI can
// distinguish "drain never started" from "drained and complete".
func (s *server) getComputeNodeDrainProgress(w http.ResponseWriter, r *http.Request, _ state.Account) {
	nodeName := r.PathValue("name")
	if nodeName == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "missing node name", "path parameter {name} is required"))
		return
	}
	node, err := s.store.ComputeNodeByName(r.Context(), nodeName)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Node not found", err.Error()))
		return
	}
	if node.Lifecycle != state.NodeLifecycleDraining &&
		node.DrainInitiatedAt == nil &&
		node.DrainCompletedAt == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "No drain in progress", "node has never been drained; lifecycle="+string(node.Lifecycle)))
		return
	}
	count := 0
	if s.store != nil {
		instances, ierr := s.store.ListInstancesByNodeID(r.Context(), node.ID)
		if ierr == nil {
			for _, ins := range instances {
				if isLiveInstance(ins) {
					continue
				}
				count++
			}
		}
	}
	writeDrainJSON(w, http.StatusOK, node, count, "ok")
}

// waitForDrainComplete is the ?wait=1 helper. It polls the row
// at drainWaitInterval up to drainWaitMaxPolls; on timeout it
// returns 200 with the current state so the operator UI can
// render a spinner rather than an error.
func (s *server) waitForDrainComplete(w http.ResponseWriter, r *http.Request, nodeName string) {
	for i := 0; i < drainWaitMaxPolls; i++ {
		select {
		case <-r.Context().Done():
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "request canceled", r.Context().Err().Error()))
			return
		case <-time.After(drainWaitInterval):
		}
		fresh, ferr := s.store.ComputeNodeByName(r.Context(), nodeName)
		if ferr != nil {
			continue
		}
		if fresh.Lifecycle != state.NodeLifecycleDraining {
			writeDrainJSON(w, http.StatusOK, fresh, 0, "ok")
			return
		}
	}
	fresh, ferr := s.store.ComputeNodeByName(r.Context(), nodeName)
	if ferr == nil {
		writeDrainJSON(w, http.StatusOK, fresh, 0, "wait-timeout")
		return
	}
	api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeCapacity, "drain wait timed out", "please poll GET"))
}

// writeDrainJSON is the canonical POST/GET 200 (and 202) response.
// The X-Drain-Status header is the only non-JSON transport
// detail; the body is always a drainResponse.
func writeDrainJSON(w http.ResponseWriter, status int, n state.ComputeNode, drainedCount int, drainStatus string) {
	resp := drainResponse{
		NodeName:             n.Name,
		Lifecycle:            string(n.Lifecycle),
		DrainInitiatedAt:     n.DrainInitiatedAt,
		DrainCompletedAt:     n.DrainCompletedAt,
		DrainedInstanceCount: drainedCount,
		LastRecoveryOutcome:  n.LastRecoveryOutcome,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Drain-Status", drainStatus)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// isLiveInstance is the dashboard-friendly predicate. We treat
// only the four non-terminal states (running, waking,
// cold_booting, parked-but-warming) as live; everything else
// counts toward drained. The exact predicate is the same one
// countObsLiveInstances uses (handlers_admin_operator_ops.go) so
// the two endpoints don't disagree on the per-node totals.
func isLiveInstance(ins state.Instance) bool {
	switch ins.State {
	case "running", "waking", "cold_booting", "parked":
		return true
	}
	return false
}
