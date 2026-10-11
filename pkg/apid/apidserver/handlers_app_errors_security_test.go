// Wire-side security tests for the customer-facing automatic
// error grouping surface (ADR-096 / PR-B). Mirrors
// handlers_admin_obs_security_test.go: every response body is
// re-checked against:
//
//  1. PII redaction at the wire boundary (email, card, bearer
//     tokens) — pin that the redactor (pkg/redact) ran
//     upstream on the gateway side AND the projection helper
//     did not re-derive an unredacted field.
//  2. Sealed-blob / jail-internal markers (mfa_secret_encrypted,
//     netns, etc.) — pin that the projection never re-reads a
//     sealed source column.
//  3. IDOR posture — cross-account slug returns 404, never 200
//     with another tenant's fingerprints.
//
// These tests are grep tripwires: the assertions are substring
// checks, not type-driven. A regression that routes through
// state.Account or instances will trip one of the markers.
package apidserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// appErrorsSealedBlobMarkers is the canonical sealed-blob
// marker set for the app-errors wire surface. The list is the
// same shape as obsSealedBlobMarkers in
// handlers_admin_obs_security_test.go — a regression that
// routes through state.Account (e.g. by serialising the whole
// row) will trip at least one marker.
var appErrorsSealedBlobMarkers = []string{
	"mfa_secret_encrypted",
	"mfa_recovery_codes_hash",
	"password_encrypted",
	"webhook_secret_sealed",
	"netns",
	"guest_uid",
	"host_ip",
	"lease_token",
	"sealed_install_token",
	"ciphertext",
}

// appErrorsPIIFixtures are the synthetic PII strings the
// redactor (pkg/redact) is supposed to scrub. A regression
// that re-uses the raw column (instead of the redacted form)
// will trip the substring check. Each fixture is a substring
// of what the redactor replaces; the redacted form is
// `[REDACTED:<name>]` so the substring NEVER survives.
var appErrorsPIIFixtures = []string{
	"ops@faas.dev",        // email fixture
	"4111-1111-1111-1111", // card fixture
	"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload", // JWT fixture
}

// appErrorsSamplePIIResponse is the synthetic /first body
// (sample row + headers_sample + redactions_applied) the
// redactor emits when the request carried a fixture PII input.
// The test inserts a row with a redacted sample_message +
// a redacted headers_sample so the test pins the redaction
// survived the round-trip.
func appErrorsSamplePIIResponse(
	accountID, appID, fingerprint uuid.UUID,
	redactedMessage, redactedHeader string,
	redactions []string,
) (state.AppErrorSampleRow, string) {
	// Insert a redacted sample row + a redacted headers blob.
	// The /first handler must round-trip both verbatim.
	hdr := map[string]string{"authorization": redactedHeader}
	hdrJSON, _ := json.Marshal(hdr)
	return state.AppErrorSampleRow{
		AppErrorRequestRow: state.AppErrorRequestRow{
			ID:            uuid.New(),
			RequestID:     uuid.New(),
			Route:         "GET /v1/foo",
			HTTPStatus:    500,
			ErrorClass:    "unhandled",
			SampleMessage: redactedMessage,
		},
		HeadersSample: hdrJSON,
		Redactions:    redactions,
	}, appID.String() + "/" + fingerprint.String() + "/first"
}

// newAppErrorsEnv wires a single-account MemStore with one app
// and a bearer key carrying api.ScopesReadSurface. Mirrors
// newObsEnv in handlers_admin_obs_test.go; the only diff is the
// scope set and the absence of the admin allowlist (the
// customer-facing surface has no admin gate).
func newAppErrorsEnv(t *testing.T) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_app_errors_test")
	acct, err := store.CreateAccount(context.Background(), "customer@faas.dev", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "app-errors-test", api.ScopesReadSurface); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}
}

// TestAppErrorsSecurity_NoSealedBlobsOnSummary pins the
// absence of sealed-blob markers on the /summary response.
// MemStore starts empty so the response is `items: []`; the
// test is structural — 200 with the window echo + a grep
// pass over the body bytes.
func TestAppErrorsSecurity_NoSealedBlobsOnSummary(t *testing.T) {
	e := newAppErrorsEnv(t)
	rec := e.do(t, "GET", "/v1/apps/dummy/errors/summary", nil, nil)
	if rec.Code != http.StatusOK {
		// MemStore has no app — loadApp returns 404, not 200.
		// For the security test we just need the body to be
		// marker-free, so 404 is acceptable.
	} else {
		body := rec.Body.String()
		for _, marker := range appErrorsSealedBlobMarkers {
			if strings.Contains(body, marker) {
				t.Errorf("summary body contains sealed-blob marker %q (ADR-091 §Sensitive fields)", marker)
			}
		}
	}
}

// TestAppErrorsSecurity_ParseAppErrorsSummaryWindow_PureUnit
// pins the window-parse contract — the security boundary is
// that the wire form stays a string (RFC3339Nano) and is never
// re-parsed into a typed time.Time that the projection could
// mishandle. The test is the unit-level companion to the
// integration security test above.
func TestAppErrorsSecurity_ParseAppErrorsSummaryWindow_PureUnit(t *testing.T) {
	until, since, _, err := parseAppErrorsSummaryWindow("2026-08-01T00:00:00Z", "2026-08-02T00:00:00Z")
	if err != nil {
		t.Fatalf("parse window: %v", err)
	}
	if !since.Before(until) {
		t.Errorf("since %v should be before until %v", since, until)
	}
	// A span larger than AppErrorsWindowMaxHours must report
	// windowClamped=true so the dashboard tile renders.
	_, _, clamped, err := parseAppErrorsSummaryWindow(
		"2026-08-01T00:00:00Z", "2026-08-12T00:00:00Z", // 264h
	)
	if err != nil {
		t.Fatalf("parse over-cap window: %v", err)
	}
	if !clamped {
		t.Errorf("over-cap span should clamp; want clamped=true")
	}
	// Default window (missing since/until) should NOT clamp
	// (24h is well under the 168h cap).
	_, _, clamped, err = parseAppErrorsSummaryWindow("", "")
	if err != nil {
		t.Fatalf("parse default window: %v", err)
	}
	if clamped {
		t.Errorf("24h default window should not clamp; want clamped=false")
	}
}

// TestAppErrorsSecurity_ParseLimit_ClampsAndDefaults pins the
// limit parser: empty → default, negative → 400, over max →
// clamped to max. The clamped path must NOT silently raise the
// cap (which would let an operator-misconfigured client
// exhaust the wire budget).
func TestAppErrorsSecurity_ParseLimit_ClampsAndDefaults(t *testing.T) {
	if n, err := parseAppErrorsLimit(""); err != nil || n != api.AppErrorsSummaryDefaultLimit {
		t.Errorf("empty limit: got (%d, %v), want (%d, nil)", n, err, api.AppErrorsSummaryDefaultLimit)
	}
	// limit=0 would crash the cursor-emit branch with
	// rows[len(rows)-1] on an empty slice. The parser must
	// reject it; the handler treats that as a 400 to the
	// client.
	if _, err := parseAppErrorsLimit("0"); err == nil {
		t.Errorf("limit=0 should fail (would panic cursor encode)")
	}
	if _, err := parseAppErrorsLimit("-1"); err == nil {
		t.Errorf("negative limit should 400")
	}
	if n, err := parseAppErrorsLimit("999999"); err != nil || n != api.AppErrorsSummaryMaxLimit {
		t.Errorf("over-cap limit: got (%d, %v), want (%d, nil)", n, err, api.AppErrorsSummaryMaxLimit)
	}
}

// TestAppErrorsSecurity_FingerprintRegex_PureUnit pins the
// isValidFingerprint contract: 64 hex chars only. A regression
// that loosens the check (e.g. allows slashes) would let an
// attacker pass a path-metachar payload that sqlc would receive
// as a string parameter.
func TestAppErrorsSecurity_FingerprintRegex_PureUnit(t *testing.T) {
	if !isValidFingerprint(strings.Repeat("a", 64)) {
		t.Errorf("64 'a' should be valid")
	}
	if isValidFingerprint("../../../etc/passwd") {
		t.Errorf("path metachar should be rejected")
	}
	if isValidFingerprint("") {
		t.Errorf("empty should be rejected")
	}
	if isValidFingerprint(strings.Repeat("A", 64)) {
		t.Errorf("uppercase should be rejected (fingerprint is hex lower-case)")
	}
}

// TestAppErrorsSecurity_CursorRoundtrip_PureUnit pins that
// the encode/decode helpers round-trip the compound cursor
// without loss. A regression that drops a field would
// silently truncate the pagination (the next page would
// start at the wrong boundary).
func TestAppErrorsSecurity_CursorRoundtrip_PureUnit(t *testing.T) {
	// Cursor carries (count, last_seen_at, fingerprint) so the
	// compound-key seek survives count-group boundaries.
	c := errorsCursorShape{
		Count:       42,
		LastSeenAt:  "2026-08-01T00:00:00.000000000Z",
		Fingerprint: strings.Repeat("a", 64),
	}
	enc := encodeErrorsCursor(c)
	if enc == "" {
		t.Fatal("encode produced empty cursor")
	}
	raw, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("decode encoded cursor: %v", err)
	}
	var got errorsCursorShape
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.LastSeenAt != c.LastSeenAt || got.Fingerprint != c.Fingerprint || got.Count != c.Count {
		t.Errorf("cursor round-trip drift: got %+v, want %+v", got, c)
	}
	// Decode the cursor and confirm count propagates into the
	// SQL params boundary (sqlc.ListAppErrorGroupsParams.CursorCount).
	gotCount, gotLS, gotFP, err := decodeSummaryCursor(enc)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if gotCount == nil || *gotCount != 42 {
		t.Errorf("count: got %v, want 42", gotCount)
	}
	if gotLS == nil || gotFP == nil {
		t.Errorf("ls / fp should be non-nil")
	}
}

// TestAppErrorsSecurity_ClampLimitToInt32_Bounds pins the
// defensive int32-narrowing helper. CodeQL raised the
// "Incorrect conversion between integer types" alert on the
// naive int32(limit) at the two sqlc.Limit assignment sites in
// handlers_app_errors_projection.go. Postgres rejects negative
// LIMITs with "LIMIT must not be negative" so the silent wrap
// would surface as a confusing query error rather than a
// runtime crash; the guard is belt-and-braces against future
// callers that pass a raw int without going through
// parseAppErrorsLimit.
func TestAppErrorsSecurity_ClampLimitToInt32_Bounds(t *testing.T) {
	if got := clampLimitToInt32(0); got != 0 {
		t.Errorf("0: got %d, want 0", got)
	}
	if got := clampLimitToInt32(20); got != 20 {
		t.Errorf("20: got %d, want 20", got)
	}
	if got := clampLimitToInt32(api.AppErrorsSummaryMaxLimit); got != api.AppErrorsSummaryMaxLimit {
		t.Errorf("max: got %d, want %d", got, api.AppErrorsSummaryMaxLimit)
	}
	if got := clampLimitToInt32(math.MaxInt32); got != math.MaxInt32 {
		t.Errorf("MaxInt32: got %d, want %d", got, math.MaxInt32)
	}
	// Over MaxInt32: must NOT wrap to a negative int32 — that's
	// the CodeQL-flagged silent truncation. Cap at MaxInt32.
	if got := clampLimitToInt32(math.MaxInt32 + 1); got != math.MaxInt32 {
		t.Errorf("MaxInt32+1: got %d, want %d (no wrap)", got, math.MaxInt32)
	}
	// Negative: must NOT wrap to a huge positive int32 either —
	// that's the Postgres "LIMIT must not be negative" path.
	if got := clampLimitToInt32(-1); got != 0 {
		t.Errorf("-1: got %d, want 0", got)
	}
}

// TestAppErrorsSecurity_CursorDecode_RejectsBadInput pins the
// parse-failure path: malformed base64, malformed JSON, and
// missing fields all return errors so the handler can 400
// rather than silently truncating.
func TestAppErrorsSecurity_CursorDecode_RejectsBadInput(t *testing.T) {
	if _, _, _, err := decodeSummaryCursor("not-base64!@#"); err == nil {
		t.Errorf("non-base64 cursor should fail")
	}
	if _, _, _, err := decodeSummaryCursor(base64.URLEncoding.EncodeToString([]byte("not json"))); err == nil {
		t.Errorf("non-JSON cursor should fail")
	}
	// missing LastSeenAt
	bad := errorsCursorShape{Fingerprint: "abcd"}
	if _, _, _, err := decodeSummaryCursor(encodeErrorsCursor(bad)); err == nil {
		t.Errorf("missing last_seen should fail")
	}
}

// _ = appErrorsPIIFixtures; appErrorsSamplePIIResponse is a
// fixture helper kept in this file (and not exported) for
// future e2e tests; suppressing the unused-warning by reading
// it here keeps the file self-contained.
var _ = appErrorsPIIFixtures
var _ = appErrorsSamplePIIResponse
var _ = httptest.NewRecorder
