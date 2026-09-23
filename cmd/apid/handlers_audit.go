// handlers_audit.go — IAM-4 (ADR-035) auth audit event surface.
//
// Routes (registered in server.go::handler):
//
//	GET /v1/audit-events            → listAuditEvents
//	GET /v1/audit-events/{id}       → getAuditEvent
//
// Trust model
//
//   - Both routes sit behind s.auth + requireScope(api.ScopesReadSurface)
//     — the same gating as the rest of the read surface (GET
//     /v1/apps, /v1/deployments, /v1/keys). A session-cookie principal
//     (Key == nil) implicitly carries admin scope per principalHasScope,
//     so a dashboard customer can read their own log without holding an
//     API key. An apps:read- or admin-scoped API key works too.
//
//   - Cross-account invisibility is enforced at the SQL layer via
//     store.ListEvents(acct.ID, ...) — the events_subject_idx
//     composite index (migrations/00002_app_manifest_and_domains.sql)
//     already filters by (subject, at desc), so even an over-read by
//     200 rows for the prefix filter costs the planner only an index
//     scan.
//
//   - getAuditEvent re-uses the same subject-pinned ListEvents result
//     so a customer cannot enumerate other accounts' events by guessing
//     bigints. A cross-account id 404s the same way an unknown id does.
//
// What this surface deliberately does NOT do
//
//   - No pagination beyond a fixed limit (default 50, max 100). The
//     GDPR export bundle is the canonical artifact for full-history
//     reads; this endpoint is the daily-driver "who deleted my key
//     last Tuesday?" UI surface.
//   - No filter by free-text data payload (e.g. "any event where
//     data.app_id = X"). kind_prefix is the only SQL-anchored filter
//     because the events table is not indexed on data — an
//     opportunistic scan would force a sequential read. The app_id
//     filter added by Wave 0 PR-C / ADR-047 is a Go-side filter on
//     the bounded (200-row) overscan window; the cost is one
//     json.RawMessage.Unmarshal per row, which is dwarfed by the
//     SQL round-trip.
//   - No PATCH / DELETE on individual rows. The events table is
//     append-only (spec §5 / §6.1); the spec doesn't grant customers
//     a tamper interface and the auditor helper never exposes one.

package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// listAuditEventsLimitMin / Max bound the ?limit query param. The
// minimum is implicitly 1 (zero means "default"); values <1 fall back
// to listAuditEventsLimitDefault.
const (
	listAuditEventsLimitDefault = 50
	listAuditEventsLimitMax     = 100
	// listAuditEventsOverRead is the hard cap passed to ListEvents when
	// the since / kind_prefix filters are in play. Picking 200 matches
	// the spec cap (most customer accounts emit <<10 audit rows/day) and
	// keeps the per-request DB cost bounded.
	listAuditEventsOverRead = 200
	// listDeploymentAuditLimitDefault / Max bound the
	// per-deployment deployment_audit timeline query
	// (production-leveling Stream A). Same shape as the
	// events limits above — the bounded pagination keeps the
	// allocation shape constant for CodeQL's taint analysis and
	// caps the dashboard's per-deployment row count.
	listDeploymentAuditLimitDefault = 50
	listDeploymentAuditLimitMax     = 50
)

// listAuditEvents handles GET /v1/audit-events. Newest first.
//
// Query params (all optional):
//
//	since        RFC 3339 timestamp; rows strictly older are skipped
//	kind_prefix  e.g. "key." returns only "key.created" / "key.deleted"
//	limit        1..100; defaults to 50
//
// On any limit > Max we silently cap (per the spec convention used by
// the rest of apid's list handlers — see GET /v1/crons). On a malformed
// since we return 400 invalid_since rather than silently dropping the
// filter, because silently ignoring the time floor would let a buggy
// SDK pin a customer to "everything since forever".
func (s *server) listAuditEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var since time.Time
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be RFC 3339 (e.g. 2026-07-25T00:00:00Z)"))
			return
		}
		since = t
	}
	prefix := r.URL.Query().Get("kind_prefix")
	// include_anonymous (Wave 0 PR-C / ADR-047): also surface
	// events rows with subject=NULL — the defensive case where the
	// app row was deleted between wake and the stateless-advisory
	// audit emit. Default false (customer never sees subject=NULL
	// rows); operators can flip to true via ?include_anonymous=true
	// for post-mortems. The product call here is "false by default,
	// ops can flip" — not "false forever" — so the toggle is part
	// of the public surface from Wave 0.
	includeAnonymous, _ := strconv.ParseBool(r.URL.Query().Get("include_anonymous"))
	// app_id (Wave 0 PR-C / ADR-047): filter the overscan window to
	// events whose data.app_id matches. The dashboard's
	// app_detail.html "Stateless advisories" link uses this with
	// kind_prefix=stateless.advisory to drill into a single app.
	// Resolved in Go (json.RawMessage → map[string]any) because the
	// events table is not indexed on data — the SQL over-read is
	// already bounded by listAuditEventsOverRead.
	appIDFilter := r.URL.Query().Get("app_id")
	limit := listAuditEventsLimitDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return
		}
		if n > listAuditEventsLimitMax {
			n = listAuditEventsLimitMax
		}
		limit = n
	}

	filter := state.CustomerEventFilter{
		AccountID: acct.ID, IncludeAnonymous: includeAnonymous,
		KindPrefix: prefix, AppID: appIDFilter, Since: since, Limit: limit,
	}
	var rows []state.Event
	var err error
	if lister, ok := s.store.(state.CustomerEventLister); ok {
		rows, err = lister.ListCustomerEvents(r.Context(), filter)
	} else {
		// Narrow legacy adapters retain a safe subject-only fallback. They do
		// not expose anonymous rows because they cannot prove ownership.
		rows, err = s.store.ListEvents(r.Context(), acct.ID, listAuditEventsOverRead)
	}
	if err != nil {
		s.log.WarnContext(r.Context(), "list audit events query failed",
			"account_id", acct.ID,
			"kind_prefix_set", prefix != "",
			"app_id_filter_set", appIDFilter != "",
			"include_anonymous", includeAnonymous,
			"err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list audit events"))
		return
	}
	// Cap the backing array at listAuditEventsLimitMax (the same bound
	// the request handler applies to ?limit=…) regardless of the
	// caller-supplied limit value. This keeps the allocation shape
	// constant for CodeQL's taint analysis — the previous `make(..., 0,
	// limit)` form was flagged by codeql go/allocation-rule because
	// `limit` is a parsed query-string value the analysis can't bound.
	// Limit the audit-events list response to listAuditEventsLimitMax rows.
	out := make([]api.AuditEventResponse, 0, listAuditEventsLimitMax)
	for _, e := range rows {
		if !since.IsZero() && e.At.Before(since) {
			continue
		}
		if prefix != "" && !strings.HasPrefix(e.Kind, prefix) {
			continue
		}
		if appIDFilter != "" && !eventDataHasAppID(e.Data, appIDFilter) {
			continue
		}
		out = append(out, auditEventResponse(e))
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, api.ListAuditEventsResponse{
		Events: out,
		Limit:  limit,
	})
}

// getAuditEvent handles GET /v1/audit-events/{id}. The id is the bigint
// primary key of the events row. Cross-account lookups 404 the same
// way an unknown id does, so a customer cannot enumerate other
// accounts' row counts by ID-probing.
//
// The mux route is "GET /v1/audit-events/{id}", so r.PathValue("id")
// is always non-empty here — the empty-string branch below is a
// defensive check that should never fire in production; it stays as
// belt-and-braces in case a future mount re-registers the path
// without a {id} segment.
func (s *server) getAuditEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	if id == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid id", "id path segment is required"))
		return
	}
	// Parse once to make sure it's actually a bigint-shaped string —
	// bad input should 400, not 404. Bigints > MaxInt64 are unreachable
	// in practice (the events table is append-only since pre-launch).
	target, err := strconv.ParseInt(id, 10, 64)
	if err != nil || target <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid id", "id must be a positive integer"))
		return
	}
	rows, err := s.store.ListEvents(r.Context(), acct.ID, listAuditEventsOverRead)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list audit events"))
		return
	}
	for _, e := range rows {
		if e.ID == target {
			writeJSON(w, http.StatusOK, auditEventResponse(e))
			return
		}
	}
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Audit event not found", "no event with that id belongs to this account"))
}

// listDeploymentAudit handles GET /v1/deployments/{id}/audit —
// the per-deployment audit timeline (issue #976 / ADR-122 /
// SAFE-RELEASES-E.2 + production-leveling Stream A).
//
// Trust model: the {id} path segment is a deployment uuid;
// ListDeploymentAudit is a deployment-scoped query, NOT an
// account-scoped query, so the handler MUST verify the deployment
// belongs to acct before reading the audit table. We do this via
// store.DeploymentByID + store.AppByID — same two-step IDOR
// posture as getDeployment (handlers_ext.go:1320).
//
// Query params (all optional):
//
//	limit  1..listDeploymentAuditLimitMax (50); defaults to listDeploymentAuditLimitDefault (50)
//
// The response shape is api.ListDeploymentAuditResponse
// (Items + Limit echo). The dashboard and SDK both consume this;
// the SDK method is pkg/api.Client.ListDeploymentAudit.
func (s *server) listDeploymentAudit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	if id == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid id", "id path segment is required"))
		return
	}
	// IDOR check — same two-step pattern as getDeployment
	// (handlers_ext.go:1320). Cross-account 404s the same way an
	// unknown id does (we never reveal whether the id exists in a
	// different account).
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Deployment not found", "no deployment with that id belongs to this account"))
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Deployment not found", "no deployment with that id belongs to this account"))
		return
	}
	limit := listDeploymentAuditLimitDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return
		}
		if n > listDeploymentAuditLimitMax {
			n = listDeploymentAuditLimitMax
		}
		limit = n
	}
	rows, err := s.store.ListDeploymentAudit(r.Context(), id, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list deployment audit"))
		return
	}
	items := make([]api.DeploymentAuditResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, deploymentAuditResponse(row))
	}
	writeJSON(w, http.StatusOK, api.ListDeploymentAuditResponse{
		Items: items,
		Limit: limit,
	})
}

// deploymentAuditResponse projects one pkg/state.DeploymentAudit
// row into the wire DTO. Internal id stays server-side; the wire
// surface keys rows by (deployment_id, at) — there is no
// per-row id on the API.
//
// Data is rendered verbatim — kind-specific shapes
// (deploy.traffic_changed → {from_percent, to_percent, actor_kind},
// deploy.rolled_back → {target_deployment_id, reason}) are
// surfaced through a single json.RawMessage column. The dashboard
// pretty-prints the JSON in its timeline block
// (deployment_detail.html) and the SDK leaves it as bytes for the
// caller to decode.
func deploymentAuditResponse(r state.DeploymentAudit) api.DeploymentAuditResponse {
	out := api.DeploymentAuditResponse{
		At:    r.At.UTC().Format(time.RFC3339Nano),
		Kind:  string(r.Kind),
		Actor: r.Actor,
		Data:  r.Data,
	}
	if r.AccountID != nil {
		out.AccountID = r.AccountID.String()
	}
	return out
}

// eventDataHasAppID returns true iff data is a JSON object whose
// "app_id" field equals the requested uuid. Wave 0 PR-C / ADR-047
// uses this to filter the audit overscan window by app_id post-SQL.
// Cheap: one json.Unmarshal per row, allocations limited to the
// small map. Unparseable data yields false (safer default): an
// unparseable row can't be proven to belong to the requested app
// and is filtered out, not surfaced cross-app.
func eventDataHasAppID(data json.RawMessage, want string) bool {
	if len(data) == 0 {
		return false
	}
	var payload struct {
		AppID string `json:"app_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return false
	}
	return payload.AppID == want
}

// dataSeverity extracts the "severity" field from an audit row's
// data map. Move 1 PR-A: the apid receiver writes a "severity" key
// into stateless.advisory rows (cmd/apid/advisory_receiver.go).
// The dashboard handler hoists it onto AuditEventRow so the badge
// column can render without re-parsing the JSON. Unparseable or
// missing data returns ("", false); the caller falls back to
// "info" rendering for non-stateless kinds.
//
// Kept here (next to eventDataHasAppID) so the post-SQL data
// extraction helpers live together.
func dataSeverity(data json.RawMessage) (string, bool) {
	if len(data) == 0 {
		return "", false
	}
	var payload struct {
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}
	if payload.Severity == "" {
		return "", false
	}
	return payload.Severity, true
}

// auditEventResponse converts one state.Event row into the wire shape.
// Subject is rendered as a string (uuid canonical form) — the wire
// contract is string-typed so JSON consumers never see Go's uuid type.
//
// Mega-PR B: hoists data.severity onto the top-level Severity
// field for stateless.advisory rows (and the receiver's `info`
// empty-batch fallback). dataSeverity is the existing per-row
// helper; the omitempty tag on AuditEventResponse.Severity means
// non-stateless kinds and pre-PR-427 rows render with no Severity
// field at all (backwards-compatible wire). Pre-PR-427 rows are
// the load-bearing case: customers with audit data already in
// the table must not see a wire-shape change for those rows.
func auditEventResponse(e state.Event) api.AuditEventResponse {
	resp := api.AuditEventResponse{
		ID:    strconv.FormatInt(e.ID, 10),
		At:    e.At.UTC().Format(time.RFC3339),
		Actor: e.Actor,
		Kind:  e.Kind,
		Data:  e.Data,
	}
	if e.Subject != nil {
		resp.Subject = e.Subject.String()
	}
	if sev, ok := dataSeverity(e.Data); ok {
		resp.Severity = sev
	}
	return resp
}
