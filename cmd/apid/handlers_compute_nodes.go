// handlers_compute_nodes.go — admin surface for registering and
// inspecting compute_nodes (issue #97 / ADR-025 axis 3, PR #114).
//
// Three endpoints:
//
//   POST /v1/compute-nodes          — register a new node
//   GET  /v1/compute-nodes          — list all (active + inactive)
//   GET  /v1/compute-nodes/{id}     — fetch one by id
//
// Single-box installations don't need any of this; the synthetic
// 'default-local' row seeded by migration 00024 is automatically
// heartbeated by schedd's PR #114 loop. These endpoints exist for
// operators of the multi-node cluster: registering a new vmmd
// target, inspecting fleet health (Active, LastHeartbeatAt),
// and verifying a recently-decommissioned node is still in the
// inactive state.
//
// Auth: gated behind s.auth today (customer auth). A future
// operator-role slice (#PR#115-ish) will move these behind a
// separate s.operatorAuth middleware; for v1.0 the customer auth
// is sufficient because the CLI is the only caller.

package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// computeNodeResponse is the wire shape apid returns. Decoupled
// from state.ComputeNode so the SQL columns can evolve (PR
// #114's last_heartbeat_at, future staleness flags, etc.) without
// breaking client contracts. JSON tags track the on-the-wire field
// names; the field names match the SQL columns where it makes sense
// for operator dashboards reading the response directly.
type computeNodeResponse struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	TargetURL          string `json:"target_url"`
	VPCPUs             int    `json:"vcpus"`
	MemMB              int    `json:"mem_mb"`
	MaxConcurrency     int    `json:"max_concurrency"`
	AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
	Active             bool   `json:"active"`
	LastHeartbeatAt    string `json:"last_heartbeat_at"`
	CreatedAt          string `json:"created_at"`
}

// computeNodeResponseFrom maps state.ComputeNode into the wire
// shape. LastHeartbeatAt may be the zero time for a row that has
// never been heartbeated (e.g. registration right before schedd's
// first tick lands); rendering it as the empty string keeps the
// JSON parser happy instead of surfacing Go's default time literal.
func computeNodeResponseFrom(n state.ComputeNode) computeNodeResponse {
	r := computeNodeResponse{
		ID:                 n.ID,
		Name:               n.Name,
		TargetURL:          n.TargetURL,
		VPCPUs:             n.VPCPUs,
		MemMB:              n.MemMB,
		MaxConcurrency:     n.MaxConcurrency,
		AdmissionCeilingMB: n.AdmissionCeilingMB,
		Active:             n.Active,
		CreatedAt:          n.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !n.LastHeartbeatAt.IsZero() {
		r.LastHeartbeatAt = n.LastHeartbeatAt.UTC().Format(time.RFC3339Nano)
	}
	return r
}

// createComputeNodeRequest mirrors the columns CreateComputeNode
// writes — only the operator-provided fields. ID, Active, and the
// timestamps are owned by the store.
type createComputeNodeRequest struct {
	Name               string `json:"name"`
	TargetURL          string `json:"target_url"`
	VPCPUs             int    `json:"vcpus"`
	MemMB              int    `json:"mem_mb"`
	MaxConcurrency     int    `json:"max_concurrency"`
	AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
}

// createComputeNode handles POST /v1/compute-nodes. Validates
// name + target_url shape, delegates to store.CreateComputeNode,
// surfaces conflicts as 409. The idempotent middleware wraps this
// route so a retried request after a partial response gets the
// existing row rather than a 409 (PR #114 closes the
// "is-the-heartbeat-working?" loop for ops).
func (s *server) createComputeNode(w http.ResponseWriter, r *http.Request, _ state.Account) {
	var req createComputeNodeRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", err.Error()))
		return
	}
	if req.Name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid name", "name is required"))
		return
	}
	if _, err := wire.ParseTarget(req.TargetURL); err != nil {
		// PR #95 dial targets must parse cleanly; a malformed
		// target_url means schedd will fail to dial later.
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid target_url", err.Error()))
		return
	}
	if req.MaxConcurrency <= 0 || req.VPCPUs <= 0 || req.MemMB <= 0 || req.AdmissionCeilingMB <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid capacity",
			"vcpus, mem_mb, max_concurrency, admission_ceiling_mb must all be positive"))
		return
	}
	out, err := s.store.CreateComputeNode(ctx(r), state.ComputeNode{
		Name:               req.Name,
		TargetURL:          req.TargetURL,
		VPCPUs:             req.VPCPUs,
		MemMB:              req.MemMB,
		MaxConcurrency:     req.MaxConcurrency,
		AdmissionCeilingMB: req.AdmissionCeilingMB,
		Active:             true, // new nodes start active; heartbeat takes over from there
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Name taken",
				fmt.Sprintf("another compute_node already uses name %q", req.Name)))
			return
		}
		api.WriteProblem(w, api.ErrCapacity(fmt.Sprintf("create compute_node: %v", err)))
		return
	}
	writeJSON(w, http.StatusCreated, computeNodeResponseFrom(out))
}

// listComputeNodes handles GET /v1/compute-nodes. Returns ALL
// rows, not just Active, so an operator can see recently-dead
// nodes that haven't been deleted yet (the heartbeat's
// MarkComputeNodeInactive flip is a soft state change; the row
// stays for audit). Pagination is deferred — the fleet is
// single-digit for v1.0, and the cluster already gates on
// admission ceilings.
func (s *server) listComputeNodes(w http.ResponseWriter, r *http.Request, _ state.Account) {
	all, err := s.store.ListAllComputeNodes(ctx(r))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity(fmt.Sprintf("list compute_nodes: %v", err)))
		return
	}
	out := make([]computeNodeResponse, 0, len(all))
	for _, n := range all {
		out = append(out, computeNodeResponseFrom(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out})
}

// getComputeNode handles GET /v1/compute-nodes/{id}. Looks up by
// the internal ID (not the operator-friendly Name), matching the
// path the rest of the API uses. Returns 404 on unknown id.
func (s *server) getComputeNode(w http.ResponseWriter, r *http.Request, _ state.Account) {
	id := r.PathValue("id")
	n, err := s.store.ComputeNodeByID(ctx(r), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Not found", "no compute_node with that id"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity(fmt.Sprintf("get compute_node: %v", err)))
		return
	}
	writeJSON(w, http.StatusOK, computeNodeResponseFrom(n))
}