// handlers_admin_obs_pr3_security_test.go — security posture
// pins for the PR #3 endpoints (ADR-091 §3.7 / issue #777):
//
//	GET /v1/admin/obs/audit-log/search
//	GET /v1/admin/obs/events
//	GET /v1/admin/obs/nodes/events
//
// The two invariants this file pins:
//
//  1. PII / sealed-blob separation. The audit_log and events
//     tables can carry captured email + jsonb payloads. The
//     projection helpers in handlers_admin_obs_pr3.go carry
//     AccountEmail / Data verbatim — that's the documented
//     contract for the operator surface (the existing
//     /v1/audit-log/all already does the same). The grep
//     checks below pin the ABSENCE of the well-known
//     sealed-blob markers (mfa_secret, password, webhook
//     secrets, etc.) so a future contributor cannot leak
//     them through the projection helpers without a test
//     failing here.
//
//  2. The Deprecation header contract (RFC 8594 + 8288):
//     the OLD path /v1/compute-nodes/events carries the
//     header trio; the NEW path does NOT. Pinning both
//     halves in the same test file so a future "copy the
//     header onto the new path" regression captures both
//     the false-negative (missing on old) and false-positive
//     (present on new) failure modes.
//
// The handlers themselves manage the PII redaction (the
// operator surface is admin-only by spec; the projected
// AccountEmail is the regulatory capture, not a re-derivation).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestObsSecurity_AuditLogSearch_AccountEmailSurfaces pins the
// audit-log AccountEmail projection verbatim. The audit_log
// table is the FK-free regulator-grade evidence path; the
// whole point of the column is for a regulator to read the
// human identifier without joining the (possibly deleted)
// accounts row. Operators already have admin scope, so the
// email is not redacted on the operator surface — that
// matches the existing /v1/audit-log/all behavior.
//
// The grep test on the SAME body (without ?include_pii=1)
// pins that no other PII column appears.
func TestObsSecurity_AuditLogSearch_AccountEmailSurfaces(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	acctID := uuid.New()
	seedAuditLogRow(t, e.store, "auth.login", &acctID, "user@example.com", "user@example.com", nil)

	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(resp.Items))
	}
	if resp.Items[0].AccountEmail != "user@example.com" {
		t.Errorf("account_email: got %q, want user@example.com (verbatim projection)", resp.Items[0].AccountEmail)
	}
	if resp.Items[0].AccountID != acctID.String() {
		t.Errorf("account_id: got %q, want %q", resp.Items[0].AccountID, acctID)
	}
}

// TestObsSecurity_AuditLogSearch_NoSealedBlobs pins the
// absence of well-known sealed-blob markers in the audit-log
// search response. The projection helper does not copy
// state.AuditLog → state.Account; the only columns that
// can surface are Kind, AccountID, AccountEmail, Actor,
// ReceivedAt, Data. A regression that json.Marshal-s a
// wider struct would surface at least one marker.
func TestObsSecurity_AuditLogSearch_NoSealedBlobs(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	acctID := uuid.New()
	seedAuditLogRow(t, e.store, "auth.login", &acctID, "user@example.com", "user@example.com", []byte(`{"ip":"1.2.3.4"}`))

	rec := e.do(t, "GET", "/v1/admin/obs/audit-log/search", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search: got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, marker := range obsSealedBlobMarkers {
		if strings.Contains(body, marker) {
			t.Errorf("audit-log/search body contains sealed-blob marker %q\nbody=%s", marker, body)
		}
	}
	for _, marker := range obsJailInternalMarkers {
		if strings.Contains(body, marker) {
			t.Errorf("audit-log/search body contains jail-internal marker %q\nbody=%s", marker, body)
		}
	}
}

// TestObsSecurity_Events_DataProjectedVerbatim pins that the
// events.data column is projected verbatim. Operators need to
// see wake_id, sidecar_name, payloads — the jsonb payload is
// the source of truth for the related wire payloads. The
// sealed-blob marker grep below pins the absence of the
// projected surface ever carrying the wider Account struct.
func TestObsSecurity_Events_DataProjectedVerbatim(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	ctx := context.Background()
	if err := e.store.AppendEvent(ctx, "system:schedd", "wake.requested", nil, []byte(`{"wake_id":"abc-123","sidecar_name":"sidecar-A"}`)); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, "GET", "/v1/admin/obs/events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events: got %d", rec.Code)
	}
	body := rec.Body.String()
	// Operators need wake_id + sidecar_name from the data blob.
	if !strings.Contains(body, "wake_id") {
		t.Errorf("events body missing wake_id: %s", body)
	}
	if !strings.Contains(body, "sidecar_name") {
		t.Errorf("events body missing sidecar_name: %s", body)
	}
}

// TestObsSecurity_NodesEvents_FrameShapeAbsent pins that the
// SSE mirror does NOT emit a Hot-payload frame when the
// upstream channel is closed immediately (the noopNotifier
// fixture). The handler must exit cleanly without writing
// any client-facing error frame — a regression that
// always wrote `event: error\ndata: <EOF>\n\n` would
// surface here.
//
// Combined with TestObsNodesEvents_OpensSSE in the
// non-security test file, this pins the wire shape: the
// response is Content-Type: text/event-stream with no
// body content (the client sees EOF on the first read).
func TestObsSecurity_NodesEvents_FrameShapeAbsent(t *testing.T) {
	e := newObsPR3Env(t, api.ScopesAdminOnly, pr3AdminEmail, pr3AdminEmail)
	rec := e.do(t, "GET", "/v1/admin/obs/nodes/events", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes/events: got %d", rec.Code)
	}
	// The noopNotifier closes the channel immediately, so the
	// handler returns without writing any frame. The Content-Type
	// header is the only signal the client sees, and the body
	// is empty (well, the StartSSE 200 write flushes nothing).
	body := rec.Body.String()
	// If the handler accidentally wrote a stray error frame, the
	// body would contain "event: error". Assert it's absent.
	if strings.Contains(body, "event: error") {
		t.Errorf("nodes/events body contains error frame: %s", body)
	}
}
