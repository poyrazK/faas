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
//	GET    /v1/compute-nodes/{name}     — operator-safe detail + live count
//	POST   /v1/compute-nodes            — upsert by name (admin POST)
//	DELETE /v1/compute-nodes/{name}     — enqueue retirement;
//	                                       ?hard=1 removes unused retired rows
//
// Errors: RFC 7807 via api.WriteProblem. Retirement and hard deletion refuse
// the synthetic default-local row. Hard deletion also requires a retired row
// with no app or instance references.

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
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
type computeNodePayload = api.ComputeNodeEnrollmentRequest

// computeNodeResponse retains the package-local name used by the handler tests
// while sharing the authenticated operator wire contract with gregalectl.
type computeNodeResponse = api.ComputeNodeOperatorResponse

// toComputeNodeResponse projects a state.ComputeNode to the wire
// shape. Kept as a free function so tests can construct responses
// from a MemStore and assert JSON equality without dragging the
// handler's auth gate through.
func toComputeNodeResponse(n state.ComputeNode) computeNodeResponse {
	r := computeNodeResponse{
		ID:                 n.ID,
		Name:               n.Name,
		TargetURL:          n.TargetURL,
		GatewayTargetURL:   stringValue(n.GatewayTargetURL),
		VPCPUs:             n.VPCPUs,
		MemMB:              n.MemMB,
		MaxConcurrency:     n.MaxConcurrency,
		AdmissionCeilingMB: n.AdmissionCeilingMB,
		Active:             n.Active,
		Lifecycle:          string(effectiveNodeLifecycle(n)),
		Role:               n.Role,
		Region:             n.Region,
		Zone:               n.Zone,
		ReleaseID:          n.ReleaseID,
		ManifestHash:       n.ManifestHash,
		CertFingerprint:    n.CertFingerprint,
		Generation:         n.Generation,
		CreatedAt:          n.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"),
	}
	if !n.LastHeartbeatAt.IsZero() {
		r.LastHeartbeatAt = n.LastHeartbeatAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00")
	}
	return r
}

// getComputeNode returns one full operator-safe node row plus the live instance
// count used by gregalectl show. The host certificate remains server-side.
func (s *server) getComputeNode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	allowed, prob := s.adminAllows(acct)
	if !allowed {
		api.WriteProblem(w, prob)
		return
	}
	row, err := s.store.ComputeNodeByName(r.Context(), r.PathValue("name"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such compute_node")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read compute node"))
		return
	}
	instances, err := s.store.ListInstancesOnNodeID(r.Context(), row.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list node instances"))
		return
	}
	response := toComputeNodeResponse(row)
	live := 0
	for _, instance := range instances {
		if state.IsLive(strings.ToLower(instance.State)) {
			live++
		}
	}
	response.LiveInstanceCount = &live
	writeJSON(w, http.StatusOK, response)
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
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
	p, reason, problem := decodeComputeNodeEnrollment(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	node, existed, err := s.prepareComputeNodeEnrollment(r.Context(), p)
	if errors.Is(err, errComputeNodeRetired) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "node_retired",
			"Node is retired", "retired node names cannot be re-enrolled; register a new node identity"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
			"Lookup failed", err.Error()))
		return
	}
	row, err := s.store.UpsertComputeNodeFromOperator(r.Context(), node)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "node_retired",
			"Node is retired", "retired node names cannot be re-enrolled; register a new node identity"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
			"Upsert failed", err.Error()))
		return
	}
	s.emitComputeNodeEnrollmentAudit(r, acct, row, reason, existed, p.DeferActivation)
	response := toComputeNodeResponse(row)
	if traceID := middleware.TraceIDFrom(r); traceID != nil {
		response.TraceID = *traceID
	}
	writeJSON(w, http.StatusOK, response)
}

var errComputeNodeRetired = errors.New("compute node is retired")

func decodeComputeNodeEnrollment(r *http.Request) (computeNodePayload, string, *api.Problem) {
	var p computeNodePayload
	if err := decodeJSON(r, &p); err != nil {
		return p, "", api.NewProblem(http.StatusBadRequest, "bad_request", "Bad JSON", err.Error())
	}
	if p.Name == "" {
		return p, "", api.NewProblem(http.StatusBadRequest, "bad_request", "Missing name", "name is required")
	}
	if p.TargetURL == "" {
		return p, "", api.NewProblem(http.StatusBadRequest, "bad_request", "Missing target_url", "target_url is required (unix:///... or tcp://...)")
	}
	if value := strings.TrimSpace(p.ScheddTargetURL); value != "" {
		if _, _, err := parseGatewayTargetURL(value); err != nil {
			detail := strings.ReplaceAll(err.Error(), "gateway_target_url", "schedd_target_url")
			return p, "", api.NewProblem(http.StatusBadRequest, "bad_request", "Invalid schedd_target_url", detail)
		}
	}
	if value := strings.TrimSpace(p.GatewayTargetURL); value != "" {
		if err := validateGatewayTargetURL(value); err != nil {
			return p, "", api.NewProblem(http.StatusBadRequest, "bad_request", "Invalid gateway_target_url", err.Error())
		}
	}
	if p.VPCPUs <= 0 || p.MemMB <= 0 || p.MaxConcurrency <= 0 || p.AdmissionCeilingMB <= 0 {
		return p, "", api.NewProblem(http.StatusBadRequest, "bad_request", "Invalid capacity",
			"vpcpus, mem_mb, max_concurrency, admission_ceiling_mb must all be > 0")
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "operator_node_enroll"
	}
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		return p, "", api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid reason", "reason must match [a-z0-9_]{1,64}")
	}
	return p, reason, nil
}

func (s *server) prepareComputeNodeEnrollment(ctx context.Context, p computeNodePayload) (state.ComputeNode, bool, error) {
	lifecycle := state.NodeLifecycleActive
	if p.DeferActivation {
		lifecycle = state.NodeLifecycleUnavailable
	}
	node := state.ComputeNode{
		Name: p.Name, TargetURL: p.TargetURL, VPCPUs: p.VPCPUs,
		MemMB: p.MemMB, MaxConcurrency: p.MaxConcurrency,
		AdmissionCeilingMB: p.AdmissionCeilingMB,
		VCPUBudget:         p.VPCPUs * api.CPUOvercommit,
		Lifecycle:          lifecycle,
	}
	if value := strings.TrimSpace(p.ScheddTargetURL); value != "" {
		node.ScheddTargetURL = &value
	}
	if value := strings.TrimSpace(p.GatewayTargetURL); value != "" {
		node.GatewayTargetURL = &value
	}
	existing, err := s.store.ComputeNodeByName(ctx, p.Name)
	if errors.Is(err, state.ErrNotFound) {
		return node, false, nil
	}
	if err != nil {
		return state.ComputeNode{}, false, err
	}
	if effectiveNodeLifecycle(existing) == state.NodeLifecycleRetired {
		return state.ComputeNode{}, false, errComputeNodeRetired
	}
	preserveComputeNodeEnrollmentMetadata(&node, existing)
	return node, true, nil
}

func preserveComputeNodeEnrollmentMetadata(node *state.ComputeNode, existing state.ComputeNode) {
	node.VCPUBudget = existing.VCPUBudget
	node.Region = existing.Region
	node.Zone = existing.Zone
	if node.ScheddTargetURL == nil {
		node.ScheddTargetURL = existing.ScheddTargetURL
	}
	if node.GatewayTargetURL == nil {
		node.GatewayTargetURL = existing.GatewayTargetURL
	}
	node.PublicIp = existing.PublicIp
	node.PublicIpSetAt = existing.PublicIpSetAt
	node.ReleaseID = existing.ReleaseID
	node.ManifestHash = existing.ManifestHash
	node.HostCertificate = existing.HostCertificate
	node.CertFingerprint = existing.CertFingerprint
	node.Role = existing.Role
	node.Generation = existing.Generation
}

func (s *server) emitComputeNodeEnrollmentAudit(r *http.Request, acct state.Account, row state.ComputeNode, reason string, existed, deferred bool) {
	if s.audit == nil {
		return
	}
	action := "created"
	if existed {
		action = "updated"
	}
	subject := row.ID
	s.audit.Emit(r.Context(), "operator.action.node_enroll", &subject, map[string]any{
		"actor": acct.ID, "node_id": row.ID, "node_name": row.Name,
		"action": action, "reason": reason, "defer_activation": deferred,
	})
}

func validateGatewayTargetURL(raw string) error {
	_, _, err := parseGatewayTargetURL(raw)
	return err
}

// parseGatewayTargetURL is the shared validation seam for the operator
// registration path and the Prometheus HTTP-SD producer. Keeping both callers
// on the same parser prevents a node from being accepted by the CRUD API and
// then silently disappearing from the metrics target set.
func parseGatewayTargetURL(raw string) (host, port string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", fmt.Errorf("gateway_target_url must be tcp://host:port: %w", err)
	}
	if u.Scheme != "tcp" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("gateway_target_url must be tcp://host:port")
	}
	host, port, err = net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return "", "", fmt.Errorf("gateway_target_url must be tcp://host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", fmt.Errorf("gateway_target_url must use a port between 1 and 65535")
	}
	// A loopback or wildcard endpoint cannot be reached by the control-plane
	// Prometheus and would otherwise be accepted by the CRUD API before the
	// HTTP-SD producer drops it. Hostnames are checked as well as IP literals:
	// localhost is commonly used as a loopback alias in hand-written operator
	// payloads and can make Prometheus scrape the control plane itself.
	normalizedHost := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if normalizedHost == "localhost" || strings.HasSuffix(normalizedHost, ".localhost") {
		return "", "", fmt.Errorf("gateway_target_url must not use a loopback hostname")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
		return "", "", fmt.Errorf("gateway_target_url must not use a loopback or wildcard address")
	}
	return host, port, nil
}

// deleteComputeNode preserves the legacy route while replacing its unsafe
// active=false toggle with the durable retirement workflow. Hard deletion is
// limited to unused rows that have already reached the terminal state.
func (s *server) deleteComputeNode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.Query().Get("hard") != "1" {
		s.enqueueObsNodeMutation(w, r, acct, "retire", false)
		return
	}
	s.hardDeleteRetiredComputeNode(w, r, acct)
}

func (s *server) hardDeleteRetiredComputeNode(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	reason, ok := parseNodeDeleteConfirmation(w, r)
	if !ok {
		return
	}
	row, err := s.store.ComputeNodeByName(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, "no such compute_node")
		return
	}
	if row.Name == state.DefaultLocalNodeName || effectiveNodeLifecycle(row) != state.NodeLifecycleRetired {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "node_not_deletable", "Node cannot be deleted", "only non-default, retired compute nodes can be hard-deleted"))
		return
	}
	instances, err := s.store.ListInstancesOnNodeID(r.Context(), row.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect node history"))
		return
	}
	apps, err := s.store.ListAppsByNodeID(r.Context(), row.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect node ownership"))
		return
	}
	if len(instances) > 0 || len(apps) > 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "node_has_history", "Node has workload history", "hard deletion is limited to unused mistaken enrollments"))
		return
	}
	if err := s.store.DeleteComputeNode(r.Context(), row.ID); errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "node_has_history", "Node has workload history", "hard deletion is limited to unused mistaken enrollments"))
		return
	} else if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal", "Delete failed", err.Error()))
		return
	}
	s.emitComputeNodeDeleteAudit(r, acct, row, reason)
	w.WriteHeader(http.StatusNoContent)
}

func parseNodeDeleteConfirmation(w http.ResponseWriter, r *http.Request) (string, bool) {
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if r.URL.Query().Get("confirm") != "true" || reason == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirmation required", "?confirm=true and an explicit reason are required for hard deletion"))
		return "", false
	}
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid reason", "reason must match [a-z0-9_]{1,64}"))
		return "", false
	}
	return reason, true
}

func (s *server) emitComputeNodeDeleteAudit(r *http.Request, acct state.Account, row state.ComputeNode, reason string) {
	if s.audit == nil {
		return
	}
	subject := row.ID
	s.audit.Emit(r.Context(), "operator.action.node_delete", &subject, map[string]any{
		"actor": acct.ID, "node_id": row.ID, "node_name": row.Name,
		"reason": reason, "result": "deleted",
	})
}
