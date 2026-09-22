// Projection helpers for the customer-facing automatic error
// grouping surface (ADR-096 / PR-B). Mirrors the file split used
// by handlers_admin_obs.go + handlers_admin_obs_projection.go:
// the HTTP handlers in handlers_app_errors.go stay ≤50 lines
// (CLAUDE.md "Handlers ≤ 50 lines — extract") and delegate all
// sqlc → wire DTO conversion to the helpers here so the
// sensitive-field omissions are in one place. The wire-side
// grep tripwires in handlers_app_errors_security_test.go pin
// every omission below.
//
// Sensitive fields NEVER projected on this surface
// (ADR-091 §"Sensitive fields (never exposed)" + ADR-096 §4.6
// "no PII in the wire form"):
//
//   - accounts.mfa_secret_encrypted, mfa_recovery_codes_hash
//   - account_passwords.hash
//   - api_keys.key_sha256, sessions.binding_hash, sessions.issued_ip
//   - app_secrets.ciphertext, app_envs.value
//   - app_webhooks.webhook_secret_sealed,
//     alert_rules.webhook_secret_sealed
//   - instances.netns, guest_uid, host_ip, lease_token
//   - invoices.raw (provider payload)
//
// The data flowing through the app-errors tables is already PII-
// redacted at write time (gatewayd-internal → apid gRPC passes
// through pkg/redact.Apply + ApplyHeaders, see
// cmd/gatewayd-internal/app_errors_recorder.go). The projection
// never re-reads the unredacted columns; it also never touches
// the sealed-blob tables listed above.
package main

import (
	"encoding/json"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// clampLimitToInt32 narrows a Go-int limit to int32 with an
// explicit upper-bound guard. CodeQL flags naive int32(n) as
// "Incorrect conversion between integer types" because Go's
// `int` is arch-dependent (64-bit on amd64) and the wrap-around
// to a negative int32 is silent. The handler-side
// parseAppErrorsLimit caps at api.AppErrorsSummaryMaxLimit=100
// so the guard is structurally redundant in production; the
// projection helper keeps it anyway so a future caller passing
// a raw int (e.g. a test, a CLI command) cannot crash the query
// with a negative LIMIT (Postgres rejects negative LIMITs with
// "LIMIT must not be negative" rather than wrapping).
func clampLimitToInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n)
}

// buildAppErrorsSummaryParams assembles the sqlc list-params for
// the summary endpoint. The cursor tuple is
// (last_seen_at, fingerprint) — distinct from the operator's
// (created_at, id) cursor at pkg/cursor/cursor.go and from the
// drill-down's (received_at, request_id) cursor at
// buildAppErrorRequestsParams below. The compound (last_seen_at,
// fingerprint) ordering matches the ORDER BY on the underlying
// query so the SQL predicate
//
//	($cursor_last_seen::timestamptz IS NULL OR (last_seen_at, fingerprint) < ($cursor_last_seen, $cursor_fingerprint))
//
// is a stable compound-keyseek. When the cursor is empty we
// pass a zero-value pgtype.Timestamptz with Valid=false so the
// predicate short-circuits to TRUE and the scan starts at the
// beginning of the (count, last_seen_at, fingerprint) order.
//
// sqlc.arg() annotations on queries.sql force the cursor
// fingerprint slot to be string-typed (sqlc otherwise infers
// both slots as timestamptz from the leading (last_seen_at)
// reference).
//
// accountID / appID are accepted as strings (state.Account.ID
// and state.App.ID are typed strings in the domain) and parsed
// to uuid.UUID here so the handler doesn't have to thread two
// types. A parse failure surfaces as a 500 from the handler.
func buildAppErrorsSummaryParams(
	accountID, appID string,
	since, until time.Time,
	cursorCount *int64,
	cursorLastSeenAt *time.Time,
	cursorFingerprint *string,
	limit int,
) sqlc.ListAppErrorGroupsParams {
	curC := int64(0)
	if cursorCount != nil {
		curC = *cursorCount
	}
	curLS := pgtype.Timestamptz{}
	if cursorLastSeenAt != nil {
		curLS = pgtype.Timestamptz{Time: *cursorLastSeenAt, Valid: true}
	}
	curFP := ""
	if cursorFingerprint != nil {
		curFP = *cursorFingerprint
	}
	return sqlc.ListAppErrorGroupsParams{
		AccountID:         pgtypeFromUUIDString(accountID),
		AppID:             pgtypeFromUUIDString(appID),
		Since:             pgtype.Timestamptz{Time: since, Valid: true},
		Until:             pgtype.Timestamptz{Time: until, Valid: true},
		CursorCount:       curC,
		CursorLastSeen:    curLS,
		CursorFingerprint: curFP,
		Limit:             clampLimitToInt32(limit),
	}
}

// buildAppErrorRequestsParams assembles the sqlc list-params for
// the drill-down endpoint. The cursor tuple is
// (received_at, request_id). Like the summary endpoint, an
// empty cursor leaves the cursor columns zero-valued so the SQL
// predicate short-circuits to TRUE.
func buildAppErrorRequestsParams(
	accountID, appID string,
	fingerprint string,
	cursorReceivedAt *time.Time,
	cursorRequestID *uuid.UUID,
	limit int,
) sqlc.ListAppErrorRequestsParams {
	curRA := pgtype.Timestamptz{}
	if cursorReceivedAt != nil {
		curRA = pgtype.Timestamptz{Time: *cursorReceivedAt, Valid: true}
	}
	curRI := pgtype.UUID{}
	if cursorRequestID != nil {
		curRI = pgtype.UUID{Bytes: *cursorRequestID, Valid: true}
	}
	return sqlc.ListAppErrorRequestsParams{
		AccountID:        pgtypeFromUUIDString(accountID),
		AppID:            pgtypeFromUUIDString(appID),
		Fingerprint:      fingerprint,
		CursorReceivedAt: curRA,
		CursorRequestID:  curRI,
		Limit:            clampLimitToInt32(limit),
	}
}

// buildAppErrorSampleParams assembles the single-row params for
// the /first endpoint. No cursor — the query is bounded by
// `LIMIT 1` inside the sqlc file (see pkg/state/queries.sql
// GetAppErrorSample).
func buildAppErrorSampleParams(accountID, appID string, fingerprint string) sqlc.GetAppErrorSampleParams {
	return sqlc.GetAppErrorSampleParams{
		AccountID:   pgtypeFromUUIDString(accountID),
		AppID:       pgtypeFromUUIDString(appID),
		Fingerprint: fingerprint,
	}
}

// pgtypeFromUUIDString parses a string-typed UUID (the
// state.Account.ID / state.App.ID representation) to
// pgtype.UUID. Returns a zero-value with Valid=false when the
// input is empty or malformed — the receiving sqlc queries
// treat VALID=false as a NULL parameter which is the correct
// "no cursor" / "no id" sentinel.
func pgtypeFromUUIDString(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

// projectAppErrorSummaryRows converts the typed AppErrorGroup
// slice (pgtype already converted at the pgstore boundary) to
// the wire DTO list. All RFC3339Nano strings; HTTP status is a
// plain int32 because the route template is on the wire as a
// string. The SampleMessage is forwarded verbatim — the
// redactor already ran at write time so the wire never sees
// unredacted PII, but the projection never re-reads the
// raw message from a sealed source.
func projectAppErrorSummaryRows(rows []state.AppErrorGroup) []api.AppErrorSummaryItem {
	out := make([]api.AppErrorSummaryItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.AppErrorSummaryItem{
			Fingerprint:             r.Fingerprint,
			ErrorClass:              r.ErrorClass,
			Route:                   r.Route,
			HTTPStatus:              r.HTTPStatus,
			Count:                   r.Count,
			RequestCount:            r.RequestCount,
			FirstSeenAt:             r.FirstSeenAt.UTC().Format(time.RFC3339Nano),
			LastSeenAt:              r.LastSeenAt.UTC().Format(time.RFC3339Nano),
			SampleMessage:           r.SampleMessage,
			LastInstanceID:          r.LastInstanceID,
			LastNodeID:              r.LastNodeID,
			LastRegion:              r.LastRegion,
			LastCommitSHA:           r.LastCommitSHA,
			LastDeploymentTag:       r.LastDeploymentTag,
			LastDeploymentCreatedAt: r.LastDeploymentCreatedAt,
			LastImageDigest:         r.LastImageDigest,
		})
	}
	return out
}

// projectAppErrorRequestRows converts the typed drill-down rows
// to the wire DTO list. The DeploymentID is nullable (the
// fingerprint row may outlive a deployment under tenant deletion
// — the FK is ON DELETE SET NULL per the migration). nil → ""
// so the wire DTO's omitempty renders it as absent.
func projectAppErrorRequestRows(rows []state.AppErrorRequestRow) []api.AppErrorRequestItem {
	out := make([]api.AppErrorRequestItem, 0, len(rows))
	for _, r := range rows {
		item := api.AppErrorRequestItem{
			RequestID:     r.RequestID.String(),
			ReceivedAt:    r.ReceivedAt.UTC().Format(time.RFC3339Nano),
			Route:         r.Route,
			HTTPStatus:    r.HTTPStatus,
			ErrorClass:    r.ErrorClass,
			SampleMessage: r.SampleMessage,
		}
		if r.DeploymentID != nil {
			item.DeploymentID = r.DeploymentID.String()
		}
		item.InstanceID = r.InstanceID
		item.NodeID = r.NodeID
		item.Region = r.Region
		item.CommitSHA = r.CommitSHA
		item.DeploymentTag = r.DeploymentTag
		item.DeploymentCreatedAt = r.DeploymentCreatedAt
		item.ImageDigest = r.ImageDigest
		out = append(out, item)
	}
	return out
}

// parseHeadersSample parses the jsonb blob stored on
// app_error_requests.headers_sample. The redactor writes a
// map[string]string (limited to 8 keys by the writer; see
// pkg/redact). A malformed jsonb — e.g. an old row written
// before the redactor was tightened — is rendered as an empty
// map so the dashboard renders a "no headers captured" tile
// rather than a 500. The wire-side grep tripwire asserts the
// redacted body never carries an Authorization / Cookie value
// even on the malformed path.
func parseHeadersSample(raw []byte) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]string{}
	}
	return out
}
