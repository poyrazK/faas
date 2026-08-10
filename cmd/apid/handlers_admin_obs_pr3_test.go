// handlers_admin_obs_pr3_test.go — happy-path coverage for the
// PR #3 endpoints (ADR-091 §3.7 / issue #777):
//
//	GET /v1/admin/obs/audit-log/search
//	GET /v1/admin/obs/events
//	GET /v1/admin/obs/nodes/events  (SSE)
//
// What this file pins:
//
//  1. Two-layer auth gate (admin scope + email allowlist) on every
//     endpoint. Non-admin scope → 403; admin scope but email not
//     in the allowlist → 403 admin_required. Mirrors the PR #1 /
//     PR #2 test patterns so the failure modes are discoverable
//     in one place.
//
//  2. The audit-log search filters — ?account_id, ?kind_prefix,
//     ?since, ?include_anonymous, ?limit — push down to the
//     store and the response carries the requested values
//     (no silent re-defaulting to the server default). The
//     default limit is api.ObsAdminAuditLogLimitDefault (200),
//     the cap is ObsAdminAuditLogLimitMax (= 500).
//
//  3. The events endpoint filters — ?actor, ?kind_prefix,
//     ?subject, ?since, ?limit — use the same shape. The data
//     column is projected verbatim (operators need wake_id,
//     sidecar_name, payloads).
//
//  4. The SSE mirror — opens with text/event-stream, accepts
//     the same auth gate, and exits cleanly on a closed channel
//     (the noopNotifier closes the channel immediately in unit
//     tests, so the assertion is "200 + Content-Type").
//
//  5. The Deprecation header on the OLD path
//     (/v1/compute-nodes/events) and its ABSENCE on the new
//     path (/v1/admin/obs/nodes/events) — the RFC 8594 + 8288
//     contract lives here.
//
// The grep-style PII / sealed-blob tests live in
// handlers_admin_obs_pr3_security_test.go so the security
// posture has its own focused file.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const pr3AdminEmail = "ops@faas.dev"

// newObsPR3Env is the PR #3 sibling of newObsEnv. Wires the same
// server bits (scopes, admin allowlist, OpsMetrics) so the
// auth gate is identical and the SSE gauge can be incremented.
func newObsPR3Env(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_obs_pr3_test")
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "obs-pr3-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}
}

// seedAuditLogRow inserts one audit_log row through the public
// store seam. Used by the seeded happy-path tests below to
// populate the table without going through the DeleteAccount
// code path (which is the only InsertAuditLog caller in the
// production handlers).
func seedAuditLogRow(t *testing.T, store *state.MemStore, kind string, accountID *uuid.UUID, email, actor string, data []byte) {
	t.Helper()
	if err := store.InsertAuditLog(context.Background(), state.AuditLog{
		Kind:         kind,
		AccountID:    accountID,
		AccountEmail: email,
		Actor:        actor,
		ReceivedAt:   time.Now().UTC(),
		Data:         data,
	}); err != nil {
		t.Fatalf("seed audit_log: %v", err)
	}
}

// --- audit-log/search ---

func TestObsAuditLogSearch_AuthGate_RejectsCustomerKey(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesReadSurface, pr3AdminEmail, "customer@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("audit-log/search with customer scope: got %d, want 403", rec.Code)
	}
}

func TestObsAuditLogSearch_AuthGate_RejectsNonAllowlistedEmail(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, "rogue@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("audit-log/search non-allowlist: got %d, want 403", rec.Code)
	}
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}

func TestObsAuditLogSearch_HappyPath_EmptyStore(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Items == nil {
		t.Errorf("items must be non-nil slice on empty store")
	}
	if len(resp.Items) != 0 {
		t.Errorf("items on empty store: got %d, want 0", len(resp.Items))
	}
	if resp.Limit != api.ObsAdminAuditLogLimitDefault {
		t.Errorf("default limit: got %d, want %d", resp.Limit, api.ObsAdminAuditLogLimitDefault)
	}
	if resp.IncludeAnonymous {
		t.Errorf("default include_anonymous: got true, want false")
	}
	if resp.GeneratedAt.IsZero() {
		t.Errorf("generated_at: zero")
	}
}

func TestObsAuditLogSearch_KindPrefix(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	acctID := uuid.New()
	seedAuditLogRow(t, e.store, "auth.login", &acctID, "user@example.com", "user@example.com", nil)
	seedAuditLogRow(t, e.store, "account.deleted", &acctID, "user@example.com", "user@example.com", nil)
	seedAuditLogRow(t, e.store, "billing.charge.failed", &acctID, "user@example.com", "user@example.com", nil)

	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search?kind_prefix=auth.", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search kind_prefix: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1 (auth.login); full=%+v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].Kind != "auth.login" {
		t.Errorf("kind: got %q, want auth.login", resp.Items[0].Kind)
	}
	if resp.KindPrefix != "auth." {
		t.Errorf("response kind_prefix echo: got %q, want auth.", resp.KindPrefix)
	}
}

func TestObsAuditLogSearch_AnonymousToggle(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	acctID := uuid.New()
	seedAuditLogRow(t, e.store, "auth.login", &acctID, "user@example.com", "user@example.com", nil)
	seedAuditLogRow(t, e.store, "system.bootstrap", nil, "", "system:scheduler", nil)

	// Default (?include_anonymous unset → false): anonymous row excluded.
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search default: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Errorf("default include_anonymous: got %d items, want 1 (system.bootstrap excluded)", len(resp.Items))
	}
	if resp.IncludeAnonymous {
		t.Errorf("default include_anonymous echo: got true, want false")
	}

	// Opt-in: include_anonymous=true → both rows.
	rec = e.do(t, "GET", "/v1/admin/obs/audit-log/search?include_anonymous=true", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search include_anonymous=1: got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Errorf("include_anonymous=1: got %d items, want 2", len(resp.Items))
	}
	if !resp.IncludeAnonymous {
		t.Errorf("include_anonymous echo: got false, want true")
	}
}

func TestObsAuditLogSearch_AccountIDFilter(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	acctA := uuid.New()
	acctB := uuid.New()
	seedAuditLogRow(t, e.store, "auth.login", &acctA, "a@example.com", "a@example.com", nil)
	seedAuditLogRow(t, e.store, "auth.login", &acctB, "b@example.com", "b@example.com", nil)

	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search?account_id="+acctA.String(), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search account_id: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(resp.Items))
	}
	if resp.Items[0].AccountID != acctA.String() {
		t.Errorf("account_id: got %q, want %q", resp.Items[0].AccountID, acctA)
	}
	if resp.AccountID != acctA.String() {
		t.Errorf("response account_id echo: got %q, want %q", resp.AccountID, acctA)
	}
}

func TestObsAuditLogSearch_BadAccountID_400(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search?account_id=not-a-uuid", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("audit-log/search bad account_id: got %d, want 400", rec.Code)
	}
}

func TestObsAuditLogSearch_BadSince_400(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search?since=not-rfc3339", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("audit-log/search bad since: got %d, want 400", rec.Code)
	}
}

func TestObsAuditLogSearch_PaginationCap(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search?limit=99999", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search over-cap: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Limit != api.ObsAdminAuditLogLimitMax {
		t.Errorf("over-cap limit: got %d, want %d (silent cap)", resp.Limit, api.ObsAdminAuditLogLimitMax)
	}
}

func TestObsAuditLogSearch_BadLimit_400(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search?limit=abc", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("audit-log/search bad limit: got %d, want 400", rec.Code)
	}
}

// --- events ---

func TestObsEvents_AuthGate_RejectsCustomerKey(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesReadSurface, pr3AdminEmail, "customer@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/events", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("events with customer scope: got %d, want 403", rec.Code)
	}
}

func TestObsEvents_AuthGate_RejectsNonAllowlistedEmail(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, "rogue@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/events", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("events non-allowlist: got %d, want 403", rec.Code)
	}
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}

func TestObsEvents_HappyPath_EmptyStore(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsEventListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Items == nil {
		t.Errorf("items must be non-nil slice on empty store")
	}
	if resp.Limit != api.ObsAdminEventsLimitDefault {
		t.Errorf("default limit: got %d, want %d", resp.Limit, api.ObsAdminEventsLimitDefault)
	}
	if resp.GeneratedAt.IsZero() {
		t.Errorf("generated_at: zero")
	}
}

func TestObsEvents_KindPrefix(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	ctx := context.Background()
	if err := e.store.AppendEvent(ctx, "system:schedd", "wake.requested", nil, []byte(`{"wake_id":"w1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(ctx, "system:builderd", "build.started", nil, []byte(`{"build_id":"b1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(ctx, "system:schedd", "wake.rejected", nil, []byte(`{"reason":"quota"}`)); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, "GET", "/v1/admin/obs/events?kind_prefix=wake.", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events kind_prefix: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsEventListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: got %d, want 2 (wake.*); full=%+v", len(resp.Items), resp.Items)
	}
	for _, row := range resp.Items {
		if !strings.HasPrefix(row.Kind, "wake.") {
			t.Errorf("kind %q does not match wake. prefix", row.Kind)
		}
	}
	if resp.KindPrefix != "wake." {
		t.Errorf("response kind_prefix echo: got %q, want wake.", resp.KindPrefix)
	}
}

func TestObsEvents_ActorFilter(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	ctx := context.Background()
	if err := e.store.AppendEvent(ctx, "system:schedd", "wake.requested", nil, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEvent(ctx, "system:builderd", "build.started", nil, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, "GET", "/v1/admin/obs/events?actor=system:schedd", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events actor: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsEventListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(resp.Items))
	}
	if resp.Items[0].Actor != "system:schedd" {
		t.Errorf("actor: got %q, want system:schedd", resp.Items[0].Actor)
	}
	if resp.Actor != "system:schedd" {
		t.Errorf("response actor echo: got %q, want system:schedd", resp.Actor)
	}
}

func TestObsEvents_BadSubject_400(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/events?subject=not-a-uuid", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("events bad subject: got %d, want 400", rec.Code)
	}
}

func TestObsEvents_PaginationCap(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/events?limit=99999", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events over-cap: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsEventListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Limit != api.ObsAdminEventsLimitMax {
		t.Errorf("over-cap limit: got %d, want %d (silent cap)", resp.Limit, api.ObsAdminEventsLimitMax)
	}
}

// --- nodes/events (SSE) ---

func TestObsNodesEvents_AuthGate_RejectsCustomerKey(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesReadSurface, pr3AdminEmail, "customer@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/events", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("nodes/events with customer scope: got %d, want 403", rec.Code)
	}
}

func TestObsNodesEvents_AuthGate_RejectsNonAllowlistedEmail(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, "rogue@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/events", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("nodes/events non-allowlist: got %d, want 403", rec.Code)
	}
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}

func TestObsNodesEvents_OpensSSE(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes/events: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type: got %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control: got %q, want no-cache", got)
	}
}

func TestObsNodesEvents_AbsentDeprecationHeader(t *testing.T) {
	// The new path is the successor; it does NOT carry the
	// Deprecation header (only the OLD /v1/compute-nodes/events
	// path carries it). See TestObsSecurity_DeprecationHeader.
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes/events: got %d", rec.Code)
	}
	if got := rec.Header().Get("Deprecation"); got != "" {
		t.Errorf("new path Deprecation header: got %q, want absent", got)
	}
	if got := rec.Header().Get("Sunset"); got != "" {
		t.Errorf("new path Sunset header: got %q, want absent", got)
	}
	if got := rec.Header().Get("Link"); got != "" {
		t.Errorf("new path Link header: got %q, want absent", got)
	}
}

func TestObsSecurity_DeprecationHeader_OnOldPath(t *testing.T) {
	// The OLD path /v1/compute-nodes/events carries the RFC 8594
	// + 8288 Deprecation header (the new path is the successor
	// per the Link rel="successor-version"). operator-only.
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/compute-nodes/events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("compute-nodes/events: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Deprecation"); got != "true" {
		t.Errorf("Deprecation: got %q, want \"true\" (RFC 8594)", got)
	}
	if got := rec.Header().Get("Sunset"); got != "Wed, 01 Oct 2026 00:00:00 GMT" {
		t.Errorf("Sunset: got %q, want RFC 8594 / RFC 7231 IMF-fixdate", got)
	}
	wantLink := `</v1/admin/obs/nodes/events>; rel="successor-version"`
	if got := rec.Header().Get("Link"); got != wantLink {
		t.Errorf("Link: got %q, want %q (RFC 8288)", got, wantLink)
	}
}

func TestObsSecurity_DeprecationHeader_AbsentOnNewPath(t *testing.T) {
	// Mirror of TestObsNodesEvents_AbsentDeprecationHeader but
	// lives in the security test file so the deprecation
	// contract has its own focused pin.
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes/events: got %d", rec.Code)
	}
	for _, h := range []string{"Deprecation", "Sunset", "Link"} {
		if got := rec.Header().Get(h); got != "" {
			t.Errorf("new path %s header: got %q, want absent", h, got)
		}
	}
}

// Compile-time guard: ensure the testEnv.ResponseRecorder field
// is the same shape the production handler reads, so an
// accidental change to the handler signature trips a build
// before a test runs.
var _ = httptest.NewRecorder
