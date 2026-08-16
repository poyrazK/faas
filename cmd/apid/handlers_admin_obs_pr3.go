// Operator observability backend — PR #3 (issue #777 / ADR-091 §3.7).
//
// Endpoints added in this file:
//
//	GET /v1/admin/obs/audit-log/search   — FK-free audit_log read with structured
//	                                       filters (no free-text on data:
//	                                       doctrine matched at
//	                                       cmd/apid/handlers_audit_log.go:36-39)
//	GET /v1/admin/obs/events             — live events table read
//	                                       (distinct source of truth per
//	                                       ADR-091 §3.7.4)
//	GET /v1/admin/obs/nodes/events       — SSE stream; successor to
//	                                       /v1/compute-nodes/events which
//	                                       carries the RFC 8594 + 8288
//	                                       Deprecation header (the old path
//	                                       is wrapped by s.withDeprecation
//	                                       in cmd/apid/auth_facade.go).
//
// All three routes inherit the §3.1 two-layer gate (admin scope +
// FAAS_ADMIN_EMAILS allowlist) and §3.2 MFA requirement. PII
// redaction is the default (no `include_pii` parameter on any of
// these endpoints — the projected rows are the audit_log / events
// table verbatim, and the operator already has admin scope).
//
// ADR-091 §3.7.9 (PR #3-introduced): the Deprecation header set on
// /v1/compute-nodes/events follows RFC 8594 + 8288 (Deprecation:
// true, Sunset: 2026-10-01, Link: </v1/admin/obs/nodes/events>;
// rel="successor-version"). 410 Gone on the old path is a follow-up
// cleanup PR after one release. The new path does NOT carry the
// header.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsAuditLogSearch handles GET /v1/admin/obs/audit-log/search
// (ADR-091 §3.7 / PR #3). Filters on the FK-free audit_log table —
// same shape as the existing /v1/audit-log/all handler but
// declared on the /v1/admin/obs/* surface so the route is
// operator-only and the Allowlist gate applies.
//
// Query parameters:
//
//	?account_id=<uuid>        restricts to one account (omitted → all)
//	?kind_prefix=<prefix>     LIKE 'prefix%' on kind
//	?since=<rfc3339>          inclusive lower bound
//	?include_anonymous=<0|1>  when 1, surfaces account_id IS NULL rows
//	?limit=<n>                default 200, cap 500
//
// NO ?q= free-text on data::text — doctrine matched at
// cmd/apid/handlers_audit_log.go:36-39 (the audit_log table has
// no GIN index on data; a free-text filter would be a sequential
// scan with no DLQ guard). ADR-091 §3.7.1 amended 2026-08-10.
func (s *server) obsAuditLogSearch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	since, kindPrefix, includeAnon, limit, prob := parseObsAuditLogSearchQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var accountFilter *uuid.UUID
	if raw := r.URL.Query().Get("account_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid account_id", "account_id must be a UUID"))
			return
		}
		accountFilter = &parsed
	}
	rows, err := s.store.ListAuditLog(r.Context(), state.AuditLogFilter{
		AccountID:        accountFilter,
		KindPrefix:       kindPrefix,
		Since:            since,
		IncludeAnonymous: includeAnon,
		Limit:            limit,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list audit log"))
		return
	}
	items := toObsAuditLogRows(rows)
	// Truncate to the requested limit (the over-read constants are
	// passed to the store so the planner picks the index scan; the
	// handler just enforces the wire cap).
	if len(items) > limit {
		items = items[:limit]
	}
	acctIDStr := ""
	if accountFilter != nil {
		acctIDStr = accountFilter.String()
	}
	windowHours := 0
	if !since.IsZero() {
		windowHours = int(time.Since(since).Hours())
	}
	writeJSON(w, http.StatusOK, api.ObsAuditLogSearchResponse{
		GeneratedAt:      time.Now().UTC(),
		Items:            items,
		Limit:            limit,
		IncludeAnonymous: includeAnon,
		WindowHours:      windowHours,
		KindPrefix:       kindPrefix,
		AccountID:        acctIDStr,
	})
}

// obsEvents handles GET /v1/admin/obs/events (ADR-091 §3.7 / PR #3).
// Reads the live events table (NOT audit_log — distinct source of
// truth per ADR-091 §3.7.4). The filter set mirrors the SQL
// shape: actor + kind_prefix + subject + since + limit.
//
// Query parameters:
//
//	?actor=<text>        exact match (omitted → all)
//	?kind_prefix=<prefix> LIKE 'prefix%' on kind
//	?subject=<uuid>     exact match (omitted → all)
//	?since=<rfc3339>    inclusive lower bound (omitted → all)
//	?limit=<n>          default 200, cap 500
//
// PII: the events.data column can carry wake_id, sidecar_name,
// payloads. Operators need to see these (the table is the source
// of truth for the related wire payloads). No ?include_pii on
// this endpoint — the projection is verbatim across the board.
func (s *server) obsEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	since, kindPrefix, limit, prob := parseObsEventsQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	actor := r.URL.Query().Get("actor")
	subject := r.URL.Query().Get("subject")
	if subject != "" {
		if _, err := uuid.Parse(subject); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid subject", "subject must be a UUID"))
			return
		}
	}
	rows, err := s.store.ListAllEventsPaged(r.Context(), actor, kindPrefix, subject, since, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list events"))
		return
	}
	items := toObsEventRows(rows)
	windowHours := 0
	if !since.IsZero() {
		windowHours = int(time.Since(since).Hours())
	}
	writeJSON(w, http.StatusOK, api.ObsEventListResponse{
		GeneratedAt: time.Now().UTC(),
		Items:       items,
		Limit:       limit,
		WindowHours: windowHours,
		KindPrefix:  kindPrefix,
		Actor:       actor,
		Subject:     subject,
	})
}

// obsNodesEventsSSE handles GET /v1/admin/obs/nodes/events
// (ADR-091 §3.7 / PR #3). SSE mirror of the existing
// /v1/compute-nodes/events handler — same shape, plus a broader
// channel set (AppChanged + DeploymentChanged + InstanceChanged +
// ComputeNodeChanged) so the operator UI's "fleet feed" sees
// every bus event, not just node upserts.
//
// The old path keeps the same shape and carries the Deprecation
// header (set by s.withDeprecation in cmd/apid/auth_facade.go).
// This path does NOT carry the header — the new path is the
// successor (per RFC 8594 §2.1 "successor-version" rel).
func (s *server) obsNodesEventsSSE(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if ok, prob := s.adminAllows(acct); !ok {
		api.WriteProblem(w, prob)
		return
	}
	if s.ops != nil {
		s.ops.SSEClients().Inc()
		defer s.ops.SSEClients().Dec()
	}
	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)
	ch, cancel, err := s.notif.Subscribe(r.Context(), obsNodesEventsChannels)
	if err != nil {
		_, _ = fmt.Fprintf(w, "event: error\ndata: {\"message\":%q}\n\n", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	defer cancel()
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			writeSSEFrame(w, n)
			if flusher != nil {
				flusher.Flush()
			}
		case <-beat.C:
			_, _ = fmt.Fprint(w, ":\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// obsNodesEventsChannels is the channel set the operator SSE
// mirror subscribes to. The selection is the union of the
// existing /v1/compute-nodes/events (which only listens to
// compute_nodes_changed) and /v1/events (which listens to
// app_changed + deployment_changed + instance_changed). The
// operator's "fleet feed" wants every bus event the apid
// surfaces — names-spaced so a future channel-add is a one-line
// PR (string appears in tests as the channel count grows).
var obsNodesEventsChannels = []string{
	db.NotifyAppChanged,
	db.NotifyDeploymentChanged,
	db.NotifyInstanceChanged,
	db.NotifyComputeNodesChanged,
}

// parseObsAuditLogSearchQuery parses the audit-log search query
// string. Returns the parsed filter values plus a *api.Problem
// on out-of-range input. Extracted so the handler stays under
// the 50-line ceiling.
func parseObsAuditLogSearchQuery(r *http.Request) (since time.Time, kindPrefix string, includeAnon bool, limit int, prob *api.Problem) {
	q := r.URL.Query()
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, "", false, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be RFC 3339 (e.g. 2026-07-25T00:00:00Z)")
		}
		since = t
	}
	kindPrefix = q.Get("kind_prefix")
	includeAnon, _ = strconv.ParseBool(q.Get("include_anonymous"))
	limit = api.ObsAdminAuditLogLimitDefault
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return time.Time{}, "", false, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer")
		}
		if n > api.ObsAdminAuditLogLimitMax {
			n = api.ObsAdminAuditLogLimitMax
		}
		limit = n
	}
	return since, kindPrefix, includeAnon, limit, nil
}

// parseObsEventsQuery parses the events query string. Same
// shape as parseObsAuditLogSearchQuery minus the
// include_anonymous filter (every events row has an actor).
func parseObsEventsQuery(r *http.Request) (since time.Time, kindPrefix string, limit int, prob *api.Problem) {
	q := r.URL.Query()
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, "", 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be RFC 3339 (e.g. 2026-07-25T00:00:00Z)")
		}
		since = t
	}
	kindPrefix = q.Get("kind_prefix")
	limit = api.ObsAdminEventsLimitDefault
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return time.Time{}, "", 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer")
		}
		if n > api.ObsAdminEventsLimitMax {
			n = api.ObsAdminEventsLimitMax
		}
		limit = n
	}
	return since, kindPrefix, limit, nil
}

// toObsAuditLogRows projects state.AuditLog rows onto the wire
// DTOs. AccountID is rendered as a canonical UUID string via
// uuid.UUID.String() (empty for anonymous rows). AccountEmail
// is captured verbatim — the whole point of the audit_log row
// is for a regulator to read the human identifier without
// joining back to a deleted accounts row. Data is the verbatim
// JSON payload.
func toObsAuditLogRows(rows []state.AuditLog) []api.ObsAuditLogRow {
	out := make([]api.ObsAuditLogRow, 0, len(rows))
	for _, r := range rows {
		entry := api.ObsAuditLogRow{
			ID:           r.ID.String(),
			Kind:         r.Kind,
			AccountEmail: r.AccountEmail,
			Actor:        r.Actor,
			ReceivedAt:   r.ReceivedAt.UTC(),
		}
		if r.AccountID != nil {
			entry.AccountID = r.AccountID.String()
		}
		if len(r.Data) > 0 {
			entry.Data = json.RawMessage(r.Data)
		}
		out = append(out, entry)
	}
	return out
}

// toObsEventRows projects state.Event rows onto the wire DTOs.
// Subject is rendered as a canonical UUID string (empty for
// events with no subject). Data is the verbatim JSON payload —
// admins need to see wake_id, sidecar_name, payloads.
func toObsEventRows(rows []state.Event) []api.ObsEventRow {
	out := make([]api.ObsEventRow, 0, len(rows))
	for _, r := range rows {
		entry := api.ObsEventRow{
			ID:    r.ID,
			At:    r.At.UTC(),
			Actor: r.Actor,
			Kind:  r.Kind,
			Data:  json.RawMessage(r.Data),
		}
		if r.Subject != nil {
			entry.Subject = r.Subject.String()
		}
		if len(entry.Data) == 0 {
			// Normalise to nil so the JSON omits the field
			// rather than rendering "data":null on every row.
			entry.Data = nil
		}
		out = append(out, entry)
	}
	return out
}
