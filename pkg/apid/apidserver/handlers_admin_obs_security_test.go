// handlers_admin_obs_security_test.go — pins the security posture
// of the operator observability backend (issue #777 / ADR-091).
//
// The two invariants this file pins (ADR-091 §"Sensitive fields
// (never exposed)"):
//
//  1. PII redaction by default. The default tenants list/detail
//     response MUST NOT contain the caller's email address
//     (or any other account's email). The string "ops@faas.dev"
//     is the test fixture's allowlist entry AND the only
//     PII-bearing string in the test store; if the redaction
//     breaks, every obs response will include the allowlist
//     email and the grep below will fail.
//
//  2. Sealed-blob fields NEVER appear on the wire. The
//     projection helper in handlers_admin_obs_projection.go
//     is the only path that builds the wire DTOs; a regression
//     that json.Marshal-s a state.Account (instead of the
//     projection) would leak mfa_secret_encrypted,
//     mfa_recovery_codes_hash, etc. The grep checks pin the
//     absence of well-known sealed-blob markers.
//
// The PII-on-demand opt-in (include_pii=1) is exercised in
// handlers_admin_obs_test.go::TestObsListTenants_IncludePII_SurfacesEmail;
// this file covers the default-redact posture.
package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestObsSecurity_NoPIIOnDefaultTenantsList walks every row of
// the default GET /v1/admin/obs/tenants response and asserts
// the email field is empty. The projection helper returns ""
// by default; a regression that copies state.Account.Email
// unconditionally would surface here.
func TestObsSecurity_NoPIIOnDefaultTenantsList(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/tenants", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants: got status %d, want 200", rec.Code)
	}
	var resp api.ObsTenantListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, row := range resp.Items {
		if row.Email != "" {
			t.Errorf("tenants[%d].Email = %q, want \"\" (PII redaction by default)", i, row.Email)
		}
	}
	// Belt-and-braces: the wire body itself must not contain the
	// allowlist email. A future contributor who accidentally
	// re-derives email from a non-projection path will trip this
	// even if the typed struct says "".
	body := rec.Body.String()
	if strings.Contains(body, "ops@faas.dev") {
		t.Errorf("tenants body contains allowlist email: PII not redacted by default")
	}
}

// TestObsSecurity_NoPIIOnDefaultTenantDetail mirrors the list
// test for the per-tenant drawer. The single-row response must
// have an empty email field on the default include_pii=0 path.
func TestObsSecurity_NoPIIOnDefaultTenantDetail(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/tenants/"+e.acct.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants detail: got status %d, want 200", rec.Code)
	}
	var resp api.ObsTenantDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Account.Email != "" {
		t.Errorf("tenants detail.Account.Email = %q, want \"\" (PII redaction by default)", resp.Account.Email)
	}
	body := rec.Body.String()
	if strings.Contains(body, "ops@faas.dev") {
		t.Errorf("tenants detail body contains allowlist email")
	}
}

// TestObsSecurity_NoSealedBlobsOnTenantsList pins the absence
// of well-known sealed-blob markers in the tenants list
// response. A regression that json.Marshal-s a state.Account
// directly would surface the MFA sealed-blob (encoded as
// base64'd bytes) — the grep checks look for the JSON
// field name + a non-null value.
func TestObsSecurity_NoSealedBlobsOnTenantsList(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/tenants?include_pii=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenants include_pii: got status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, marker := range obsSealedBlobMarkers {
		if strings.Contains(body, marker) {
			t.Errorf("tenants body contains sealed-blob marker %q (ADR-091 §Sensitive fields)", marker)
		}
	}
}

// TestObsSecurity_NoSealedBlobsOnNodesList pins the absence
// of sealed-blob / jail-internal markers on the nodes list.
// The wire DTO (ObsNodeRow) never carries TargetURL (the vmmd
// dial target) — a regression that re-uses
// computeNodeResponse verbatim would expose it.
func TestObsSecurity_NoSealedBlobsOnNodesList(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/nodes?include_inactive=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes: got status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// target_url is the vmmd unix-socket path; surfacing it on
	// the operator surface would reveal jail internals to a
	// non-root operator (defense-in-depth — the field is
	// exposed at the /v1/compute-nodes shape but the obs
	// surface is the long-term canonical and we want it
	// sanitised from day one).
	if strings.Contains(body, "target_url") {
		t.Errorf("nodes body contains target_url (jail-internal surface)")
	}
	for _, marker := range obsJailInternalMarkers {
		if strings.Contains(body, marker) {
			t.Errorf("nodes body contains jail-internal marker %q", marker)
		}
	}
}

// obsSealedBlobMarkers is the set of JSON field names whose
// presence in a wire response indicates a sealed-blob leak.
// The list is deliberately small — a regression that copies
// the wrong struct will surface at least one marker. Adding
// new sealed columns to state.Account MUST add a marker here
// (and the grep test will fail until the projection helper
// learns to omit the new column).
var obsSealedBlobMarkers = []string{
	"mfa_secret_encrypted",
	"mfa_recovery_codes_hash",
	"password_encrypted",
	"webhook_secret_sealed",
	"sealed_install_token",
}

// obsJailInternalMarkers is the set of instance-row fields
// that must never surface on the operator wire (a leaked
// netns path or guest_uid gives an attacker a foothold on
// the host jail).
var obsJailInternalMarkers = []string{
	"netns",
	"guest_uid",
	"lease_token",
}
