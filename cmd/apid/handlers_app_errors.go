// Customer-facing automatic error grouping handlers
// (ADR-096 / PR-B). Three routes:
//
//	GET /v1/apps/{slug}/errors/summary
//	GET /v1/apps/{slug}/errors/{fingerprint}
//	GET /v1/apps/{slug}/errors/{fingerprint}/first
//
// Auth + IDOR posture mirrors getAppRoutes in handlers_routes.go
// (authLimited → requireScope(ScopesReadSurface) → loadApp).
// Cross-account slug is a 404, never a 200 with another tenant's
// fingerprints; loadApp is the load-bearing guard. The handlers
// are intentionally ≤50 lines each (CLAUDE.md "Handlers ≤ 50
// lines — extract") — every sqlc → wire DTO conversion lives in
// handlers_app_errors_projection.go so the sensitive-field
// omissions are in one place.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// errorsCursorShape is the wire shape of the opaque pagination
// cursor on the summary + drill-down endpoints. It is a JSON
// object with two fields, base64-url-encoded; the SDK never
// reads it, only round-trips it. Mirrors pkg/cursor/cursor.go
// style but uses a different compound key (last_seen_at,
// fingerprint for the summary; received_at, request_id for the
// drill-down).
type errorsCursorShape struct {
	Count       int64  `json:"c,omitempty"`
	LastSeenAt  string `json:"l,omitempty"`
	Fingerprint string `json:"f,omitempty"`
	ReceivedAt  string `json:"r,omitempty"`
	RequestID   string `json:"i,omitempty"`
}

// encodeErrorsCursor base64-url-encodes a cursor shape. Returns
// "" for the zero-value (matches the "no further page" sentinel
// contract used elsewhere in apid). The summary cursor is keyed
// on (count, last_seen_at, fingerprint); the drill-down cursor
// is keyed on (received_at, request_id) and ignores Count.
func encodeErrorsCursor(c errorsCursorShape) string {
	if c.LastSeenAt == "" && c.Fingerprint == "" &&
		c.ReceivedAt == "" && c.RequestID == "" {
		return ""
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(raw)
}

// appErrorsSummaryDefaultWindow is the fallback (since, until)
// span when the caller omits both. ADR-096 §4.3 — matches the
// dashboard's default tile view.
const appErrorsSummaryDefaultWindow = 24 * time.Hour

// getAppErrorsSummary handles
// GET /v1/apps/{slug}/errors/summary. Returns the top-N grouped
// fingerprints over [since, until]. The window is clamped to
// AppErrorsWindowMaxHours (168h) — when clamping fires,
// response.WindowClamped is true so the dashboard can render a
// "you widened the window past the cap" tile.
//
// IDOR posture: loadApp returns 404 on cross-account slug. The
// 404 is byte-identical to the "no such app" 404 so existence
// is not leaking through the auth gate.
//
// Operator-as-tenant view (P1): see handlers_obs_on_behalf_of.go.
// When ?on_behalf_of= is present, the plan gate, loadApp, the
// retention clamp (target.Plan.AppErrorsRetentionDays()), and the
// ListAppErrorGroups predicate (target.ID) all flow through the
// target's identity.
func (s *server) getAppErrorsSummary(w http.ResponseWriter, r *http.Request, acct state.Account) { //nolint:contextcheck
	target, ok := s.resolveOnBehalfOf(w, r, acct, "errors-summary")
	if !ok {
		return
	}
	authAcct := acct
	if target != nil {
		authAcct = *target
	}
	// Plan gate: per-app error surfacing is Hobby+; Free gets 402 +
	// upsell. The gate runs BEFORE loadApp so a Free customer
	// probing a Hobby+ slug never gets a 404 (slug-leak guard —
	// same posture as handlers_metrics.go:53-57 and the rest of
	// the per-app observability PR series).
	if !authAcct.Plan.AppErrorsAllowed() {
		api.WriteProblem(w, api.ErrPlanAppErrorsNotAllowed(authAcct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, authAcct, r.PathValue("slug"))
	if !ok {
		return
	}
	q := r.URL.Query()
	until, since, windowClamped, err := parseAppErrorsSummaryWindow(q.Get("since"), q.Get("until"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid window", err.Error()))
		return
	}
	// Per-plan retention clamp (ADR-096). Hobby=7d, Pro=30d,
	// Scale=90d. A customer cannot query past their plan's
	// retention window — without the clamp, a Free customer
	// who upgrades to Hobby could see 90 days of pre-Hobby
	// history that the retention cron has NOT purged yet
	// (the cron is account-scoped on the new plan only, and a
	// freshly-upgraded Free account has no Hobby-side purge
	// entry until 7d rolls over). The clamp sets WindowClamped
	// so the dashboard can render the "you widened past the
	// retention cap" tile; the existing WindowClamped bool
	// already lives on the wire shape.
	retentionCap := time.Duration(authAcct.Plan.AppErrorsRetentionDays()) * 24 * time.Hour
	if until.Sub(since) > retentionCap {
		windowClamped = true
		since = until.Add(-retentionCap)
	}
	limit, err := parseAppErrorsLimit(q.Get("limit"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid limit", err.Error()))
		return
	}
	curC, curLS, curFP, err := decodeSummaryCursor(q.Get("cursor"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid cursor", err.Error()))
		return
	}
	rows, err := s.store.ListAppErrorGroups(r.Context(), buildAppErrorsSummaryParams(
		authAcct.ID, app.ID, since, until, curC, curLS, curFP, limit,
	))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "list failed", "see request_id"))
		return
	}
	items := projectAppErrorSummaryRows(rows)
	next := ""
	if len(items) == limit {
		last := rows[len(rows)-1]
		next = encodeErrorsCursor(errorsCursorShape{
			Count:       last.Count,
			LastSeenAt:  last.LastSeenAt.UTC().Format(time.RFC3339Nano),
			Fingerprint: last.Fingerprint,
		})
	}
	if target != nil {
		emitOperatorActionView(r, s, acct, target.ID, "errors-summary")
	}
	writeJSON(w, http.StatusOK, api.AppErrorsSummaryResponse{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		AppID:         app.ID,
		AppSlug:       app.Slug,
		WindowStart:   since.UTC().Format(time.RFC3339Nano),
		WindowEnd:     until.UTC().Format(time.RFC3339Nano),
		WindowClamped: windowClamped,
		Items:         items,
		NextCursor:    next,
		Limit:         limit,
	})
}

// listAppErrorRequests handles
// GET /v1/apps/{slug}/errors/{fingerprint}. Cursor-paginated
// drill-down over the request rows that landed on this
// fingerprint. Returns 404 when the fingerprint has been
// purged by the retention cron or never existed (and the
// cross-account slug case is also 404 — loadApp handles that).
func (s *server) listAppErrorRequests(w http.ResponseWriter, r *http.Request, acct state.Account) { //nolint:contextcheck
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	fingerprint := r.PathValue("fingerprint")
	if !isValidFingerprint(fingerprint) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "fingerprint not found", "see request_id"))
		return
	}
	q := r.URL.Query()
	limit, err := parseAppErrorsLimit(q.Get("limit"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid limit", err.Error()))
		return
	}
	curRA, curRI, err := decodeDrilldownCursor(q.Get("cursor"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid cursor", err.Error()))
		return
	}
	rows, err := s.store.ListAppErrorRequests(r.Context(), buildAppErrorRequestsParams(
		acct.ID, app.ID, fingerprint, curRA, curRI, limit,
	))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "list failed", "see request_id"))
		return
	}
	if len(rows) == 0 {
		// Drill-down over a purged fingerprint OR over a
		// fingerprint that never existed for this slug. Both
		// are 404 — IDOR posture matches the loadApp 404 above.
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "fingerprint not found", "see request_id"))
		return
	}
	items := projectAppErrorRequestRows(rows)
	next := ""
	if len(items) == limit {
		last := rows[len(rows)-1]
		next = encodeErrorsCursor(errorsCursorShape{
			ReceivedAt: last.ReceivedAt.UTC().Format(time.RFC3339Nano),
			RequestID:  last.RequestID.String(),
		})
	}
	header := appErrorRequestsHeader(rows[0], fingerprint)
	writeJSON(w, http.StatusOK, api.AppErrorRequestsResponse{
		Fingerprint: header.fingerprint,
		ErrorClass:  header.errorClass,
		Route:       header.route,
		HTTPStatus:  header.httpStatus,
		Requests:    items,
		NextCursor:  next,
	})
}

// getAppErrorSample handles
// GET /v1/apps/{slug}/errors/{fingerprint}/first. Returns
// the oldest request row for the fingerprint plus the redacted
// headers_sample + the list of redactions applied. 404 when
// the fingerprint has been purged.
func (s *server) getAppErrorSample(w http.ResponseWriter, r *http.Request, acct state.Account) { //nolint:contextcheck
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	fingerprint := r.PathValue("fingerprint")
	if !isValidFingerprint(fingerprint) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "fingerprint not found", "see request_id"))
		return
	}
	row, err := s.store.GetAppErrorSample(r.Context(),
		buildAppErrorSampleParams(acct.ID, app.ID, fingerprint))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "fingerprint not found", "see request_id"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "sample lookup failed", "see request_id"))
		return
	}
	headers := parseHeadersSample(row.HeadersSample)
	redactions := row.Redactions
	if redactions == nil {
		redactions = []string{}
	}
	item := api.AppErrorRequestItem{
		RequestID:     row.RequestID.String(),
		ReceivedAt:    row.ReceivedAt.UTC().Format(time.RFC3339Nano),
		Route:         row.Route,
		HTTPStatus:    row.HTTPStatus,
		ErrorClass:    row.ErrorClass,
		SampleMessage: row.SampleMessage,
	}
	if row.DeploymentID != nil {
		item.DeploymentID = row.DeploymentID.String()
	}
	item.InstanceID = row.InstanceID
	item.NodeID = row.NodeID
	item.Region = row.Region
	item.CommitSHA = row.CommitSHA
	item.DeploymentTag = row.DeploymentTag
	item.DeploymentCreatedAt = row.DeploymentCreatedAt
	item.ImageDigest = row.ImageDigest
	writeJSON(w, http.StatusOK, api.AppErrorSampleResponse{
		AppErrorRequestItem: item,
		HeadersSample:       headers,
		RedactionsApplied:   redactions,
	})
}

// isValidFingerprint enforces the canonical 64-hex-char
// fingerprint shape so an attacker cannot slip path metachars
// (../, %, etc.) into a sqlc string parameter. The fingerprint
// is sha256-hex per ADR-096 §3.5.
func isValidFingerprint(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

// appErrorRequestsHeader is the denormalised header echoed on
// the drill-down response. The parent fingerprint is taken
// from the path value (the URL itself is the source of truth)
// and the page-level error_class / route / http_status come
// from the first row.
func appErrorRequestsHeader(row state.AppErrorRequestRow, fingerprint string) struct {
	fingerprint string
	errorClass  string
	route       string
	httpStatus  int32
} {
	return struct {
		fingerprint string
		errorClass  string
		route       string
		httpStatus  int32
	}{
		fingerprint: fingerprint,
		errorClass:  row.ErrorClass,
		route:       row.Route,
		httpStatus:  row.HTTPStatus,
	}
}

// parseAppErrorsSummaryWindow parses ?since= and ?until= as
// RFC3339Nano UTC strings. Missing ?until defaults to now().
// Missing ?since defaults to until - 24h. The returned span is
// clamped to AppErrorsWindowMaxHours (168h) — the returned bool
// is true when the clamp fires. The bool is computed against the
// PRE-CLAMP span: comparing post-clamp `until.Sub(since)` would
// always be ≤ max and the dashboard "you widened past the cap"
// tile would never render.
func parseAppErrorsSummaryWindow(sinceStr, untilStr string) (until, since time.Time, clamped bool, err error) {
	if untilStr == "" {
		until = time.Now().UTC()
	} else {
		until, err = time.Parse(time.RFC3339Nano, untilStr)
		if err != nil {
			return time.Time{}, time.Time{}, false, err
		}
		until = until.UTC()
	}
	if sinceStr == "" {
		since = until.Add(-appErrorsSummaryDefaultWindow)
	} else {
		since, err = time.Parse(time.RFC3339Nano, sinceStr)
		if err != nil {
			return time.Time{}, time.Time{}, false, err
		}
		since = since.UTC()
	}
	max := time.Duration(api.AppErrorsWindowMaxHours) * time.Hour
	if until.Sub(since) > max {
		clamped = true
		since = until.Add(-max)
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, false, errAppErrorsWindowInvalid
	}
	return until, since, clamped, nil
}

// parseAppErrorsLimit parses ?limit= with the defaults and
// caps from pkg/api/limits.go. Empty → default; 0 / negative
// → 400 (the handler uses len(items)==limit as the
// "page was filled" signal and the cursor-encode branch then
// reads rows[len(rows)-1]; limit=0 with an empty page would
// panic). Over max → clamped to max.
func parseAppErrorsLimit(s string) (int, error) {
	if s == "" {
		return api.AppErrorsSummaryDefaultLimit, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, errAppErrorsLimitInvalid
	}
	if n > api.AppErrorsSummaryMaxLimit {
		n = api.AppErrorsSummaryMaxLimit
	}
	return n, nil
}

// decodeSummaryCursor parses the opaque cursor on the summary
// endpoint. Returns (nil, nil, nil, nil) for the empty cursor —
// the SQL predicate uses IS NULL on the count column as the
// fast-path. Count is part of the (count, last_seen_at,
// fingerprint) seek tuple; without it the cursor would silently
// drop or duplicate rows at count-group boundaries (page-2 rows
// with same last_seen_at but smaller fingerprint get returned
// again).
func decodeSummaryCursor(s string) (*int64, *time.Time, *string, error) {
	if s == "" {
		return nil, nil, nil, nil
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, nil, nil, err
	}
	var c errorsCursorShape
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, nil, nil, err
	}
	if c.LastSeenAt == "" || c.Fingerprint == "" {
		return nil, nil, nil, errAppErrorsCursorShape
	}
	t, err := time.Parse(time.RFC3339Nano, c.LastSeenAt)
	if err != nil {
		return nil, nil, nil, err
	}
	fp := c.Fingerprint
	return &c.Count, &t, &fp, nil
}

// decodeDrilldownCursor parses the opaque cursor on the
// drill-down endpoint. Returns (nil, nil, nil) for the empty
// cursor.
func decodeDrilldownCursor(s string) (*time.Time, *uuid.UUID, error) {
	if s == "" {
		return nil, nil, nil
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, nil, err
	}
	var c errorsCursorShape
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, nil, err
	}
	if c.ReceivedAt == "" || c.RequestID == "" {
		return nil, nil, errAppErrorsCursorShape
	}
	t, err := time.Parse(time.RFC3339Nano, c.ReceivedAt)
	if err != nil {
		return nil, nil, err
	}
	id, err := uuid.Parse(c.RequestID)
	if err != nil {
		return nil, nil, err
	}
	return &t, &id, nil
}

// errAppErrorsWindowInvalid is the sentinel for an
// inverted or zero-length window.
var errAppErrorsWindowInvalid = &appErrorsHandlerError{msg: "window inverted"}

// errAppErrorsLimitInvalid is the sentinel for a negative limit.
var errAppErrorsLimitInvalid = &appErrorsHandlerError{msg: "limit invalid"}

// errAppErrorsCursorShape is the sentinel for a malformed cursor.
var errAppErrorsCursorShape = &appErrorsHandlerError{msg: "cursor shape invalid"}

type appErrorsHandlerError struct{ msg string }

func (e *appErrorsHandlerError) Error() string { return e.msg }
