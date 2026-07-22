// Issue #98 / ADR-028: operator-facing CRUD for compute_nodes. apid is
// the only writer to customer-intent tables (CLAUDE.md ownership), but
// compute_nodes is operator-intent (a fleet operator adds a box), and
// the issue's spec puts the surface on apid so the daemon boundary
// stays single-writer.
//
// Auth model: admin-only. The /v1/compute-nodes routes sit behind an
// email allowlist loaded from FAAS_ADMIN_EMAILS (comma-separated). An
// authenticated bearer-token caller whose AccountByKeyHash resolves
// AND whose account email is in the allowlist reaches the handler.
// Everyone else (including all customer-tier accounts) gets 403 with
// code admin_required. The allowlist is empty by default in dev so
// the routes 403 out of the box — there's no implicit "any account
// with a valid key is admin" path. Production deploys set the env
// var to the operator team's addresses.
//
// Endpoints:
//
//	GET    /v1/compute-nodes            — list (active only by default)
//	POST   /v1/compute-nodes            — upsert by name (admin POST)
//	DELETE /v1/compute-nodes/{name}     — soft-delete (active=false);
//	                                       ?hard=1 toggles to DELETE FROM
//
// Errors: RFC 7807 via api.WriteProblem. Hard-delete is admin-grade
// and refuses on the synthetic default-local row (operator foot-gun
// guard) — the canonical way to "remove" default-local is to set its
// active=false via PATCH or to drain the box, not to wipe the row that
// every pre-existing instance backfill from migration 00024 still
// references.

package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adminAllowlist is the in-memory set of operator emails allowed to
// reach the /v1/compute-nodes routes. Set by WithAdminAllowlist at
// startup; empty => all routes 403. Comparison is case-insensitive
// (operators paste emails from a config and we don't want a
// capital-letter rejection to land on a real operator).
type adminAllowlist struct {
	emails map[string]struct{}
}

// WithAdminAllowlist installs the admin email allowlist. csv is a
// comma-separated list of email addresses (whitespace trimmed,
// compared case-insensitively). An empty csv leaves the routes
// admin-disabled. Called from newServerWithDeps when FAAS_ADMIN_EMAILS
// is set.
func (s *server) WithAdminAllowlist(csv string) *server {
	if s.adminAllowlist == nil {
		s.adminAllowlist = &adminAllowlist{emails: map[string]struct{}{}}
	}
	for _, raw := range strings.Split(csv, ",") {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}
		s.adminAllowlist.emails[email] = struct{}{}
	}
	return s
}

// adminAllows reports whether acct's email is in the allowlist. nil
// allowlist = no admin access at all (every route 403). Returns the
// same RFC 7807 problem the handler writes when the answer is false,
// so the caller can write it directly without a second switch.
func (s *server) adminAllows(acct state.Account) (bool, *api.Problem) {
	if s.adminAllowlist == nil || len(s.adminAllowlist.emails) == 0 {
		return false, api.NewProblem(http.StatusForbidden, "admin_required",
			"Admin access required",
			"FAAS_ADMIN_EMAILS is empty on this deployment; no admin endpoints are reachable")
	}
	if _, ok := s.adminAllowlist.emails[strings.ToLower(acct.Email)]; !ok {
		return false, api.NewProblem(http.StatusForbidden, "admin_required",
			"Admin access required",
			"this account is not in the operator allowlist for /v1/compute-nodes")
	}
	return true, nil
}

// computeNodePayload is the JSON shape POST /v1/compute-nodes
// accepts. overlay_ip and last_heartbeat_at are not part of the
// input — overlay_ip is set by vmmd's tailscale detect at
// self-registration, and last_heartbeat_at is set by schedd's
// heartbeat goroutine. Operators pre-registering a box (before
// vmmd boots) leave overlay_ip empty; vmmd's startup will overwrite
// it via UpsertComputeNode on first contact.
type computeNodePayload struct {
	Name               string `json:"name"`
	TargetURL          string `json:"target_url"`
	VPCPUs             int    `json:"vpcpus"`
	MemMB              int    `json:"mem_mb"`
	MaxConcurrency     int    `json:"max_concurrency"`
	AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
}

// computeNodeResponse is the canonical JSON wire shape GET /
// POST / DELETE return. Mirrors state.ComputeNode with id exposed
// as a UUID string and timestamps in RFC 3339.
type computeNodeResponse struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	TargetURL          string `json:"target_url"`
	VPCPUs             int    `json:"vpcpus"`
	MemMB              int    `json:"mem_mb"`
	MaxConcurrency     int    `json:"max_concurrency"`
	AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
	Active             bool   `json:"active"`
	LastHeartbeatAt    string `json:"last_heartbeat_at,omitempty"`
	CreatedAt          string `json:"created_at"`
}

// toComputeNodeResponse projects a state.ComputeNode to the wire
// shape. Kept as a free function so tests can construct responses
// from a MemStore and assert JSON equality without dragging the
// handler's auth gate through.
func toComputeNodeResponse(n state.ComputeNode) computeNodeResponse {
	r := computeNodeResponse{
		ID:                 n.ID,
		Name:               n.Name,
		TargetURL:          n.TargetURL,
		VPCPUs:             n.VPCPUs,
		MemMB:              n.MemMB,
		MaxConcurrency:     n.MaxConcurrency,
		AdmissionCeilingMB: n.AdmissionCeilingMB,
		Active:             n.Active,
		CreatedAt:          n.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"),
	}
	if !n.LastHeartbeatAt.IsZero() {
		r.LastHeartbeatAt = n.LastHeartbeatAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00")
	}
	return r
}

// listComputeNodes handles GET /v1/compute-nodes. include_inactive=1
// surfaces drained rows so an operator can audit a box that schedd's
// watchdog just deactivated; default false hides them (the dashboard
// would otherwise show a stale "active" line that placement skips).
func (s *server) listComputeNodes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	allowed, prob := s.adminAllows(acct)
	if !allowed {
		api.WriteProblem(w, prob)
		return
	}
	includeInactive := r.URL.Query().Get("include_inactive") == "1"
	rows, err := s.store.ListComputeNodes(r.Context(), includeInactive)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
			"List failed", err.Error()))
		return
	}
	out := make([]computeNodeResponse, 0, len(rows))
	for _, n := range rows {
		out = append(out, toComputeNodeResponse(n))
	}
	writeJSON(w, http.StatusOK, out)
}

// createOrUpdateComputeNode handles POST /v1/compute-nodes. The
// payload is an UPSERT keyed on name (so re-POSTing with the same
// name acts as PATCH without us inventing a separate PATCH route).
// Validation runs first: zero-valued fields are a 400, mirroring
// vmmd's own self-registration checks (cmd/vmmd/register.go) so the
// operator and the daemon agree on what "valid" means.
func (s *server) createOrUpdateComputeNode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	allowed, prob := s.adminAllows(acct)
	if !allowed {
		api.WriteProblem(w, prob)
		return
	}
	var p computeNodePayload
	if err := decodeJSON(r, &p); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "bad_request",
			"Bad JSON", err.Error()))
		return
	}
	if p.Name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "bad_request",
			"Missing name", "name is required"))
		return
	}
	if p.TargetURL == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "bad_request",
			"Missing target_url", "target_url is required (unix:///... or tcp://...)"))
		return
	}
	// Resource-size sanity mirrors vmmd's registerComputeNode: zero
	// values are a config bug, not a meaningful "I want a node with
	// zero RAM" state. Same 400 surface so the operator's UI can
	// show the same message the daemon would.
	if p.VPCPUs <= 0 || p.MemMB <= 0 || p.MaxConcurrency <= 0 || p.AdmissionCeilingMB <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "bad_request",
			"Invalid capacity",
			"vpcpus, mem_mb, max_concurrency, admission_ceiling_mb must all be > 0"))
		return
	}
	row, err := s.store.UpsertComputeNode(r.Context(), state.ComputeNode{
		Name:               p.Name,
		TargetURL:          p.TargetURL,
		VPCPUs:             p.VPCPUs,
		MemMB:              p.MemMB,
		MaxConcurrency:     p.MaxConcurrency,
		AdmissionCeilingMB: p.AdmissionCeilingMB,
	})
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
			"Upsert failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, toComputeNodeResponse(row))
}

// deleteComputeNode handles DELETE /v1/compute-nodes/{name}. Soft
// delete by default (active=false, schedd's watchdog will not
// re-activate because the heartbeat goroutine skips non-default
// paths and the heartbeat itself stops once vmmd is gone). ?hard=1
// is a real DELETE FROM — admin foot-gun and gated on name !=
// "default-local" so an operator doesn't wipe the row every legacy
// instance backfill from migration 00024 still references.
func (s *server) deleteComputeNode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	allowed, prob := s.adminAllows(acct)
	if !allowed {
		api.WriteProblem(w, prob)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "bad_request",
			"Missing name", "path parameter name is required"))
		return
	}
	hard := r.URL.Query().Get("hard") == "1"
	if hard {
		if name == state.DefaultLocalNodeName {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "default_local_protected",
				"Cannot delete default-local",
				"the synthetic default-local node is referenced by every legacy instance; drain it (set active=false) instead of hard-deleting"))
			return
		}
		// Resolve to id first so the soft-then-hard flow uses the
		// same key (and so a missing name yields the same 404 the
		// rest of the API uses).
		row, err := s.store.ComputeNodeByName(r.Context(), name)
		if err != nil {
			s.notFound(w, "no such compute_node")
			return
		}
		if err := s.store.DeleteComputeNode(r.Context(), row.ID); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
				"Delete failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Soft-delete path: resolve, then flip active=false. The
	// compute_node_changed pg_notify trigger (migration 00026) fires
	// on the UPDATE, gatewayd evicts its per-node client cache,
	// and schedd's watchdog treats the row as drained.
	row, err := s.store.ComputeNodeByName(r.Context(), name)
	if err != nil {
		s.notFound(w, "no such compute_node")
		return
	}
	if err := s.store.SetComputeNodeActive(r.Context(), row.ID, false); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
			"Deactivate failed", err.Error()))
		return
	}
	row.Active = false
	writeJSON(w, http.StatusOK, toComputeNodeResponse(row))
}

// _ keeps the json import alive for future inline decode paths; the
// handler uses decodeJSON from server.go today.
var _ = json.Unmarshal
