// Operator observability backend (issue #777 / ADR-091).
//
// Endpoints:
//
//	GET /v1/admin/obs/overview                 — KPI bundle
//	GET /v1/admin/obs/tenants                  — paginated tenant list
//	GET /v1/admin/obs/tenants/{id}             — per-tenant drill-down
//	GET /v1/admin/obs/nodes                    — compute-node list
//	GET /v1/admin/obs/nodes/{name}/heartbeats  — per-node heartbeat window
//
// Auth model: admin-only. Every route sits behind
// `requireScope(api.ScopesAdminOnly...)` AND the email
// allowlist loaded from FAAS_ADMIN_EMAILS (same two-layer gate
// as /v1/compute-nodes and /v1/admin/billing-paddle-catalog).
// Unlike /v1/admin/billing-paddle-catalog, this surface also
// requires MFA — the obs view exposes secret-adjacent metadata
// (MFA enrollment status, account email) and the cost is a one-
// time TOTP per 24h session thanks to step-up elsewhere in the
// stack (ADR-091 §"Two-layer gate confirmed").
//
// PII policy (ADR-091 §3): the default response redacts email.
// ?include_pii=1 is the only opt-in. Every opt-in emits a
// pii.accessed audit row keyed on the caller account id; the
// caller is the audit subject (not the target account) so a
// single audit-log search surfaces every cross-account PII view
// the operator has taken.
//
// Sensitive fields (ADR-091 §"Sensitive fields (never exposed)"):
// the projection helpers in handlers_admin_obs_projection.go are
// the only path that builds a wire shape. They never carry
// sealed-blob MFA, hashed passwords, hashed tokens, sealed
// webhook / env secrets, or jail internals (netns, guest_uid,
// host_ip, lease_token). The grep tests in
// handlers_admin_obs_security_test.go pin every omission.
//
// Pagination (ADR-091 §7): default 200, hard cap 500
// (ObsAdminPaginationDefault / ObsAdminPaginationMax). The
// customer surface uses 25/100; the operator surface is larger
// because the operator UI is fleet-wide.
package apidserver

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsHeartbeatsSinceDefault is the default ?since= window for
// GET /v1/admin/obs/nodes/{name}/heartbeats when the caller
// omits the parameter. 30m covers the schedd watchdog cycle
// (default 60s) plus a margin so the operator can spot
// degrading nodes without paging through hours of history.
const obsHeartbeatsSinceDefault = 30 * time.Minute

// obsHeartbeatsSinceMax bounds the ?since= window so a buggy
// operator client cannot ask for "all heartbeats since the
// dawn of time" and OOM the apid daemon. 24h matches
// ObsAdminWindowMaxHours; any longer is clamped and the
// response sets since_clamped=true.
const obsHeartbeatsSinceMax = 24 * time.Hour

// obsHeartbeatsLimitDefault / obsHeartbeatsLimitMax bound the
// ?limit= query on the per-node heartbeat endpoint. The cap
// (2000) is the same shape as the existing
// /v1/compute-nodes/{name}/heartbeats handler so internal
// tooling can move over without retuning.
const (
	obsHeartbeatsLimitDefault = 200
	obsHeartbeatsLimitMax     = 2000
)

// obsOverview handles GET /v1/admin/obs/overview using aggregate reads
// from one consistent store snapshot.
func (s *server) obsOverview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	snapshot, err := s.store.OperatorOverview(r.Context(), time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load observability overview"))
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// obsListTenants handles GET /v1/admin/obs/tenants. Cursor
// pagination by account.created_at DESC; the cursor is a
// RFC 3339 timestamp (account id tiebreaks). PII redaction is
// enforced in the projection helper; ?include_pii=1 is the
// only opt-in and triggers a pii.accessed audit row.
func (s *server) obsListTenants(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"),
		api.ObsAdminPaginationDefault, api.ObsAdminPaginationMax, "tenants")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	includePII, _ := strconv.ParseBool(r.URL.Query().Get("include_pii"))
	if includePII {
		emitPIIAccessed(r, s, acct, "tenants")
	}
	rows, err := s.store.ListAllAccounts(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list accounts"))
		return
	}
	filtered := filterTenantRows(r, rows)
	cursor := r.URL.Query().Get("cursor")
	out, nextCursor := paginateTenantRows(filtered, cursor, limit)
	items, err := projectTenantList(r.Context(), s.store, out, includePII)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load tenant summaries"))
		return
	}
	writeJSON(w, http.StatusOK, api.ObsTenantListResponse{
		Items:      items,
		NextCursor: nextCursor,
		Limit:      limit,
	})
}

// obsGetTenant handles GET /v1/admin/obs/tenants/{id}. Returns
// 404 on unknown id; the per-tenant drawer renders without a
// second round-trip. PII redaction mirrors obsListTenants.
func (s *server) obsGetTenant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad account id", "expected UUID"))
		return
	}
	includePII, _ := strconv.ParseBool(r.URL.Query().Get("include_pii"))
	if includePII {
		emitPIIAccessed(r, s, acct, "tenants/"+targetID)
	}
	target, err := s.store.AccountByID(r.Context(), targetID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Account not found", err.Error()))
		return
	}
	detail, err := buildTenantDetail(r.Context(), s.store, target, includePII)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load tenant detail"))
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// obsListNodes handles GET /v1/admin/obs/nodes. Mirrors
// /v1/compute-nodes but with cursor pagination + the obs
// wire shape (ObsNodeListResponse). include_inactive=1
// surfaces drained rows (cmd/apid/compute_nodes.go precedent).
//
// PR #4 (ADR-092) extends the response with per-node live
// utilization: live instance counts, RAMUsedMB (the §6.2
// invariant #2 derivative), AdmissionMarginMB, and the
// latest heartbeat's CPU%/disk. The two aggregates are
// fetched in parallel goroutines — they don't share state
// and a 200ms tail on either is fine for an operator dashboard
// tile (not a customer-facing hot path).
func (s *server) obsListNodes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"),
		api.ObsAdminPaginationDefault, api.ObsAdminPaginationMax, "nodes")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	includeInactive := r.URL.Query().Get("include_inactive") == "1"
	offset := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid cursor", "cursor must be a value returned as next_cursor"))
			return
		}
	}
	ctx := r.Context()
	rows, err := s.store.ListComputeNodes(ctx, includeInactive)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list compute nodes"))
		return
	}
	// Fetch both aggregates in parallel. errgroup isn't
	// warranted — two queries, bounded count, simple wait.
	type liveResult struct {
		live []state.PerNodeStats
		err  error
	}
	type hbResult struct {
		hb  []state.ComputeNodeHeartbeatStats
		err error
	}
	liveCh := make(chan liveResult, 1)
	hbCh := make(chan hbResult, 1)
	go func() {
		live, err := s.store.PerNodeLiveStats(ctx)
		liveCh <- liveResult{live: live, err: err}
	}()
	go func() {
		hb, err := s.store.LatestHeartbeatStats(ctx)
		hbCh <- hbResult{hb: hb, err: err}
	}()
	var liveStats []state.PerNodeStats
	var hbStats []state.ComputeNodeHeartbeatStats
	for i := 0; i < 2; i++ {
		select {
		case lr := <-liveCh:
			if lr.err != nil {
				api.WriteProblem(w, api.ErrCapacity("could not aggregate per-node live stats"))
				return
			}
			liveStats = lr.live
		case hr := <-hbCh:
			if hr.err != nil {
				api.WriteProblem(w, api.ErrCapacity("could not aggregate latest heartbeat stats"))
				return
			}
			hbStats = hr.hb
		}
	}
	items, nextCursor := paginateNodes(rows, limit, liveStats, hbStats, offset)
	writeJSON(w, http.StatusOK, api.ObsNodeListResponse{
		Items:      items,
		NextCursor: nextCursor,
		Limit:      limit,
	})
}

// obsNodeHeartbeats handles GET /v1/admin/obs/nodes/{name}/heartbeats.
// The ?since= window defaults to 30m, hard-capped at 24h
// (ObsAdminWindowMaxHours); the response sets since_clamped=true
// when the cap fires. The limit defaults to 200, hard cap 2000
// — matches the existing /v1/compute-nodes/{name}/heartbeats
// shape so internal tooling can switch over without retuning.
func (s *server) obsNodeHeartbeats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad node name", "path parameter name is required"))
		return
	}
	node, err := s.store.ComputeNodeByName(r.Context(), name)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Node not found", err.Error()))
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"),
		obsHeartbeatsLimitDefault, obsHeartbeatsLimitMax, "heartbeats")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	since, clamped := parseObsSince(r.URL.Query().Get("since"))
	hbs, err := s.store.ListComputeNodeHeartbeats(r.Context(), node.ID, since, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list heartbeats"))
		return
	}
	writeJSON(w, http.StatusOK, api.ObsHeartbeatListResponse{
		NodeID:       node.ID,
		Name:         node.Name,
		Since:        since,
		SinceClamped: clamped,
		Heartbeats:   toHeartbeatRows(hbs),
		Limit:        limit,
	})
}

// parseObsSince parses the ?since= query into a time.Time.
// Returns the effective since + a clamped flag. The clamp
// fires when the caller passed a since older than
// obsHeartbeatsSinceMax; the response surfaces
// since_clamped=true so the operator UI can render "clamped
// to 24h" without re-deriving from the wire timestamps.
func parseObsSince(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Now().UTC().Add(-obsHeartbeatsSinceDefault), false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		// Malformed ?since= defaults to the default window —
		// the operator UI surfaces the same 400 in the future
		// when we tighten the contract.
		return time.Now().UTC().Add(-obsHeartbeatsSinceDefault), false
	}
	floor := time.Now().UTC().Add(-obsHeartbeatsSinceMax)
	if t.Before(floor) {
		return floor, true
	}
	return t, false
}

// emitPIIAccessed writes a pii.accessed audit row on every
// cross-account PII opt-in. The audit subject is the caller
// account id (NOT the target) so a single audit-log search
// surfaces every PII view the operator has taken. The endpoint
// field names the route so the operator can correlate the row
// with the request that produced it.
//
// No-op when s.audit is nil (unit tests that don't wire the
// audit pipeline), matching the nil-tolerant default of
// loadOrgAuditFrom in auth_facade.go.
func emitPIIAccessed(r *http.Request, s *server, caller state.Account, endpoint string) {
	if s == nil || s.audit == nil {
		return
	}
	cid := caller.ID
	s.audit.Emit(r.Context(), "pii.accessed", &cid, map[string]any{
		"endpoint": endpoint,
		"actor":    caller.ID,
	})
}

// emitOperatorActionView writes an operator.action.view audit row
// whenever an admin reads a tenant's data via ?on_behalf_of= (P1).
// The audit subject is the TARGET account id (so the target's
// audit-log feed shows every operator view), and the actor field
// captures the calling admin so the admin's own audit log can
// also be filtered to "what did I read on behalf of tenants".
//
// `endpoint` should be the route name (e.g. "metrics", "usage").
// `targetID` is the resolved target account's id (NOT slug).
//
// No-op when s.audit is nil, matching emitPIIAccessed's
// nil-tolerant default.
func emitOperatorActionView(r *http.Request, s *server, caller state.Account, targetID, endpoint string) {
	if s == nil || s.audit == nil {
		return
	}
	tid := targetID
	s.audit.Emit(r.Context(), "operator.action.view", &tid, map[string]any{
		"actor":             caller.ID,
		"endpoint":          endpoint,
		"target_kind":       "account",
		"target_account_id": targetID,
	})
}

// emitOperatorActionReclaimBuild writes an operator.action.reclaim_build
// audit row when an admin sweeps stuck-running builds via the P2c
// endpoint. accountID is nil because the sweep is fleet-level (no
// single tenant owns the operation).
func emitOperatorActionReclaimBuild(r *http.Request, s *server, caller state.Account, olderThanSeconds, sweptCount int, thresholdISO, reason string) {
	if s == nil || s.audit == nil {
		return
	}
	s.audit.Emit(r.Context(), "operator.action.reclaim_build", nil, map[string]any{
		"actor":              caller.ID,
		"older_than_seconds": olderThanSeconds,
		"swept_count":        sweptCount,
		"threshold_iso":      thresholdISO,
		"reason":             reason,
	})
}

// obsNodeWakeLatency handles GET /v1/admin/obs/nodes/wake-latency
// (PR #4 / ADR-092 §3.6). Surfaces per-node wake-latency
// quantiles (p50, p95, p99) over a 5-minute window by PromQL-
// evaluating the new labelled histogram
// `gateway_wake_latency_seconds_by_node`. Also emits the fleet
// p95 from the existing unlabeled histogram so the operator can
// compare per-node against the §12 fleet number in the same
// scrape. The handler uses the same histogramQuantile helper
// the per-app latency handler uses, just keyed by node_id
// instead of app. The "__unknown" bucket (wake that lost its
// target) is filtered out so the per-node rows don't include
// stranded observations.
//
// nil-safe: if s.promqlClient is nil (single-node dev / no
// Prometheus sidecar) we return an empty Quantiles slice +
// fleet_p95_ms=0 instead of 500 — the dashboard tile shows
// "no data" rather than a red error banner.
func (s *server) obsNodeWakeLatency(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	const window = "5m"
	now := time.Now().UTC()
	if s.promqlClient == nil {
		writeJSON(w, http.StatusOK, api.ObsNodeWakeLatencyResponse{
			Window:    window,
			AsOf:      now,
			Quantiles: []api.ObsWakeLatencyQuantile{},
		})
		return
	}
	// Per-node buckets: one QueryGrouped call returns
	// map[node_id]map[le]cum. Increase yields sample counts in the window; we
	// don't have to iterate per node.
	nodeBuckets, err := s.promqlClient.QueryGrouped(r.Context(),
		fmt.Sprintf(`sum by (node_id, le)(increase(gateway_wake_latency_seconds_by_node_bucket{node_id!="__unknown"}[%s]))`, window), "node_id", "le")
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not evaluate per-node wake latency"))
		return
	}
	// Fleet p95 — same window so the per-node numbers are
	// directly comparable to the fleet number.
	fleetV, err := s.promqlClient.QueryScalar(r.Context(), appmetrics.HistogramQuantileMSQuery(
		0.95,
		fmt.Sprintf(`sum by (le)(rate(gateway_wake_latency_seconds_bucket[%s]))`, window),
		fmt.Sprintf(`sum(rate(gateway_wake_latency_seconds_count[%s]))`, window)))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not evaluate fleet wake p95"))
		return
	}
	// Resolve node_id → node_name. ListComputeNodes is bounded
	// (tens of nodes); we walk the list once.
	rows, err := s.store.ListComputeNodes(r.Context(), true)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list compute nodes for wake-latency resolution"))
		return
	}
	idToName := make(map[string]string, len(rows))
	for _, n := range rows {
		idToName[n.ID] = n.Name
	}
	out := make([]api.ObsWakeLatencyQuantile, 0, len(nodeBuckets))
	for nodeID, buckets := range nodeBuckets {
		var sampleCount int64
		for _, cum := range buckets {
			if int64(math.Round(cum)) > sampleCount {
				sampleCount = int64(math.Round(cum))
			}
		}
		out = append(out, api.ObsWakeLatencyQuantile{
			NodeID:      nodeID,
			NodeName:    idToName[nodeID],
			P50MS:       appmetrics.SafeFloat(histogramQuantile(0.50, buckets) * 1000),
			P95MS:       appmetrics.SafeFloat(histogramQuantile(0.95, buckets) * 1000),
			P99MS:       appmetrics.SafeFloat(histogramQuantile(0.99, buckets) * 1000),
			SampleCount: sampleCount,
		})
	}
	// Sort by node name asc so the operator UI doesn't reorder
	// rows between scrapes (it currently groups by name
	// alphabetically in the dashboard tile).
	sort.Slice(out, func(i, j int) bool { return out[i].NodeName < out[j].NodeName })
	writeJSON(w, http.StatusOK, api.ObsNodeWakeLatencyResponse{
		Window:              window,
		Quantiles:           out,
		FleetP95MS:          appmetrics.SafeFloat(fleetV),
		AsOf:                now,
		PrometheusAvailable: true,
	})
}
