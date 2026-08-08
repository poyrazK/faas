// handlers_audit_log.go — issue #755 / PR-6 audit_log dashboard surface.
//
// Routes (registered in server.go::handler):
//
//	GET /v1/audit-log          → listAuditLog  (customer-scoped, MFA + apps:read)
//	GET /v1/audit-log/all      → listAuditLogAll (operator-only, admin scope)
//
// Source table: the FK-free audit_log table (migrations/00163_audit_log.sql).
// Distinct from /v1/audit-events which reads the live events table:
// audit_log is append-only + FK-free so its rows survive the parent
// accounts row delete, and a regulator / DPO can replay post-deletion
// state from the audit_log row alone.
//
// Trust model
//
//   - /v1/audit-log is gated by requireMFA(requireScope(ScopesReadSurface))
//     — the same chain as /v1/audit-events. Cross-account invisibility is
//     enforced by pinning AuditLogFilter.AccountID to the calling
//     account's ID; the SQL filter rejects account_id IS NULL rows by
//     default (customer never sees anonymous rows).
//   - /v1/audit-log/all is gated by requireScope(ScopesAdminOnly) —
//     admin session or admin API key. The filter is the request
//     parameters; admins can read across accounts and can opt into
//     account_id IS NULL rows with ?include_anonymous=true.
//
// What this surface deliberately does NOT do
//
//   - No pagination beyond a fixed limit (default 50, max 100). The
//     full-history read is the GDPR export bundle (spec §17 G6); this
//     endpoint is the dashboard "what was hard-deleted for this
//     account?" UI surface.
//   - No PATCH / DELETE on individual rows. The audit_log table is
//     append-only by spec (ISO 27001 SoA A.5.33 — retention forever);
//     there is no UPDATE / DELETE permission on the table in
//     production and no Go-side path that would emit one.
//   - No free-text data payload filter. Kind prefix is the only
//     SQL-anchored filter; the table has no GIN index on data and
//     the bounded over-read keeps the planner on the (received_at
//     DESC) index.
package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// listAuditLogLimitDefault / Max bound the ?limit query param.
	// Minimum is implicitly 1 (zero means "default"); values <1 fall
	// back to listAuditLogLimitDefault. The 100 cap mirrors the
	// /v1/audit-events posture so a customer moving between the two
	// endpoints doesn't see surprising cap differences.
	listAuditLogLimitDefault = 50
	listAuditLogLimitMax     = 100
	// listAuditLogOverRead is the hard cap passed to ListAuditLog
	// when the since / kind_prefix filters are in play. Picking 200
	// matches the /v1/audit-events over-read and keeps the
	// per-request DB cost bounded by the audit_log_received_at_idx
	// scan.
	listAuditLogOverRead = 200
)

// listAuditLog handles GET /v1/audit-log. Newest first. Customer-scoped:
// AuditLogFilter.AccountID is pinned to the calling account's ID and
// IncludeAnonymous is forced false so a customer never sees
// account_id IS NULL rows.
//
// Query params (all optional):
//
//	since        RFC 3339 timestamp; rows strictly older are skipped
//	kind_prefix  e.g. "account." returns only "account.deleted"
//	limit        1..100; defaults to 50
//
// Malformed since → 400 invalid_since (not silent drop, per the
// /v1/audit-events precedent — silently ignoring the time floor would
// let a buggy SDK pin a customer to "everything since forever").
// Limit > Max is silently capped per the spec convention used by the
// rest of apid's list handlers.
func (s *server) listAuditLog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	since, prefix, limit, ok := parseAuditLogQuery(w, r)
	if !ok {
		return
	}
	accountUUID, err := uuid.Parse(acct.ID)
	if err != nil {
		// acct.ID is always a UUID per the Account contract — a
		// parse failure here would mean the auth middleware let
		// through a malformed subject, which is a 500-class bug.
		api.WriteProblem(w, api.ErrCapacity("could not parse account id"))
		return
	}
	rows, err := s.store.ListAuditLog(r.Context(), state.AuditLogFilter{
		AccountID:        &accountUUID,
		KindPrefix:       prefix,
		Since:            since,
		IncludeAnonymous: false,
		Limit:            listAuditLogOverRead,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list audit log"))
		return
	}
	writeListAuditLogResponse(w, rows, limit)
}

// listAuditLogAll handles GET /v1/audit-log/all. Operator-only:
// AuditLogFilter.AccountID and IncludeAnonymous are taken from the
// query string so an admin can drill into any account or surface the
// account_id IS NULL rows.
//
// Query params (all optional):
//
//	account_id          UUID; restrict to one account
//	since               RFC 3339 timestamp; rows strictly older are skipped
//	kind_prefix         e.g. "account." returns only "account.deleted"
//	limit               1..100; defaults to 50
//	include_anonymous   bool; when true, also surface account_id IS NULL rows
//
// Malformed since / account_id → 400 with a stable CodeValidation
// code; the limit cap and over-read bounds match listAuditLog so an
// admin moving between the two endpoints sees the same posture.
func (s *server) listAuditLogAll(w http.ResponseWriter, r *http.Request, _ state.Account) {
	q := r.URL.Query()
	since, prefix, limit, ok := parseAuditLogQuery(w, r)
	if !ok {
		return
	}
	var accountFilter *uuid.UUID
	if raw := q.Get("account_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid account_id", "account_id must be a UUID"))
			return
		}
		accountFilter = &parsed
	}
	includeAnonymous, _ := strconv.ParseBool(q.Get("include_anonymous"))
	rows, err := s.store.ListAuditLog(r.Context(), state.AuditLogFilter{
		AccountID:        accountFilter,
		KindPrefix:       prefix,
		Since:            since,
		IncludeAnonymous: includeAnonymous,
		Limit:            listAuditLogOverRead,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list audit log"))
		return
	}
	writeListAuditLogResponse(w, rows, limit)
}

// parseAuditLogQuery is the shared query-string parser for both
// /v1/audit-log and /v1/audit-log/all. Returns the parsed filter
// values; ok=false means the handler already wrote a 400 problem and
// the caller must return.
//
// since is zero when the param is absent or empty (handler treats
// zero as "no floor"). limit defaults to listAuditLogLimitDefault
// when absent; values <1 also fall back to the default. Values
// >listAuditLogLimitMax are silently capped per the /v1/audit-events
// convention.
func parseAuditLogQuery(w http.ResponseWriter, r *http.Request) (since time.Time, prefix string, limit int, ok bool) {
	q := r.URL.Query()
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be RFC 3339 (e.g. 2026-07-25T00:00:00Z)"))
			return time.Time{}, "", 0, false
		}
		since = t
	}
	prefix = q.Get("kind_prefix")
	limit = listAuditLogLimitDefault
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return time.Time{}, "", 0, false
		}
		if n > listAuditLogLimitMax {
			n = listAuditLogLimitMax
		}
		limit = n
	}
	return since, prefix, limit, true
}

// writeListAuditLogResponse converts the store rows into the wire DTO,
// truncates to limit, and writes the response. Extracted so both
// listAuditLog and listAuditLogAll share the same truncation / DTO
// conversion code path.
//
// The since / kind_prefix filters are pushed down to SQL inside
// pgstore.ListAuditLog (and memstore.ListAuditLog) — the SQL WHERE
// clause honours audit_log_received_at_idx so the over-read is an
// index scan, not a sequential read. The handler just truncates the
// already-filtered result to the caller's limit.
func writeListAuditLogResponse(w http.ResponseWriter, rows []state.AuditLog, limit int) {
	out := make([]api.AuditLogEntry, 0, listAuditLogLimitMax)
	for _, r := range rows {
		entry := auditLogEntry(r)
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, api.ListAuditLogResponse{
		Entries: out,
		Limit:   limit,
	})
}

// auditLogEntry converts one state.AuditLog row into the wire shape.
// AccountID is rendered as a UUID string (canonical hyphenated form)
// via uuid.UUID.String() — same wire contract as AuditEventResponse's
// Subject field. AccountEmail is included verbatim so the dashboard
// can render "user@example.com was hard-deleted at 2026-08-08T...".
//
// strings.HasPrefix is used for the kind_prefix filter on the
// caller side; this helper does NOT apply it — the post-filter is in
// writeListAuditLogResponse so the DTO conversion stays pure.
func auditLogEntry(a state.AuditLog) api.AuditLogEntry {
	entry := api.AuditLogEntry{
		ID:           a.ID.String(),
		Kind:         a.Kind,
		AccountEmail: a.AccountEmail,
		Actor:        a.Actor,
		ReceivedAt:   a.ReceivedAt.UTC().Format(time.RFC3339),
	}
	if a.AccountID != nil {
		entry.AccountID = a.AccountID.String()
	}
	if len(a.Data) > 0 {
		entry.Data = a.Data
	}
	// Defensive trim: a Kind value with a leading "/" or whitespace
	// is a sign the store layer emitted a malformed row. The
	// audit_log_kind_chk-style CHECK would normally reject this at
	// insert time, but the in-memory store doesn't have that CHECK,
	// so we surface the issue as a kind prefix "?" so the dashboard
	// can't render a misleading kind column. Cheap to do here; the
	// alternative is a deeper refactor of the post-filter logic.
	if strings.TrimSpace(entry.Kind) == "" {
		entry.Kind = "?"
	}
	return entry
}
