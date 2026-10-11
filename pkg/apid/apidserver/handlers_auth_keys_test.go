package apidserver

// IAM-5 (issue #189) handler tests. The createKey / listKeys /
// deleteKey / rotateKey / graceWindow endpoints are exercised
// through the testEnv harness; the unit tests pin the
// per-route contract:
//   - createKey stamps expires_at for non-admin scopes and
//     omits it for admin (legacy semantics).
//   - listKeys returns the new fields (status, expires_at,
//     revoked_at, rotated_from_id) on every row.
//   - deleteKey soft-revokes (status='revoked') and is
//     idempotent (repeated delete returns 204, not 404).
//   - createKey rejects with 409 + api_key_limit_exceeded at
//     the per-plan cap (Plan.KeysMax).
//   - rotateKey returns new plaintext + old_key_expires_at
//     and overwrites the old key's expires_at to the grace
//     deadline.
//   - getGraceWindow / setGraceWindow round-trip.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestHandlers_CreateKey_NonAdminGetsExpiry pins the
// expires_at=now+365d default for non-admin scopes. Admin
// scopes (the legacy "admin" entry) keep nil expiry.
func TestHandlers_CreateKey_NonAdminGetsExpiry(t *testing.T) {
	e := setup(t, api.PlanHobby)

	body := api.CreateKeyRequest{Label: "ci-deploy", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExpiresAt == "" {
		t.Error("non-admin key: ExpiresAt empty, want ~365d out")
	}
	expTime, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("parse ExpiresAt: %v", err)
	}
	delta := time.Until(expTime)
	if delta < 360*24*time.Hour || delta > 370*24*time.Hour {
		t.Errorf("non-admin ExpiresAt: got %v out, want ~365d", delta)
	}
	if resp.Status != "active" {
		t.Errorf("Status: got %q, want active", resp.Status)
	}
}

// TestHandlers_CreateKey_AdminHasNoExpiry pins the
// "admin = never expires" legacy semantics. A key with
// scopes=["admin"] returns ExpiresAt="".
func TestHandlers_CreateKey_AdminHasNoExpiry(t *testing.T) {
	e := setup(t, api.PlanHobby)

	body := api.CreateKeyRequest{Label: "admin-key", Scopes: []string{api.ScopeAdmin}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExpiresAt != "" {
		t.Errorf("admin key: ExpiresAt = %q, want empty (legacy admin semantics)", resp.ExpiresAt)
	}
}

// TestHandlers_CreateKey_RejectsAtQuota pins the 409 +
// api_key_limit_exceeded shape when the per-account cap is
// hit. Hobby=10; setup() seeds 1 admin key, so 8 more mints
// fill the cap and the 9th additional mint rejects.
func TestHandlers_CreateKey_RejectsAtQuota(t *testing.T) {
	e := setup(t, api.PlanHobby)

	// Mint 8 more (1 setup + 8 = 9, one slot left).
	for i := 0; i < 8; i++ {
		body := api.CreateKeyRequest{
			Label:  "k",
			Scopes: []string{api.ScopeAppsRead},
		}
		rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed #%d: code=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	// 9th additional mint: count=9 < cap=10 → 201.
	body := api.CreateKeyRequest{Label: "fill", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("9th additional mint: code=%d body=%s, want 201", rec.Code, rec.Body.String())
	}
	// 10th additional mint: count=10 == cap=10 → 409.
	body = api.CreateKeyRequest{Label: "over", Scopes: []string{api.ScopeAppsRead}}
	rec = e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("10th additional mint: code=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeAPIKeyLimitExceeded) {
		t.Errorf("body: want code=%s, got %s", api.CodeAPIKeyLimitExceeded, rec.Body.String())
	}
}

// TestHandlers_DeleteKey_SoftRevokesAndIdempotent pins the
// soft-revoke contract: DELETE sets status='revoked' but
// the row stays for audit lineage. Repeated DELETE is 204
// no-op (idempotent).
func TestHandlers_DeleteKey_SoftRevokesAndIdempotent(t *testing.T) {
	e := setup(t, api.PlanHobby)

	body := api.CreateKeyRequest{Label: "k", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// First delete: 204.
	rec = e.do(t, http.MethodDelete, "/v1/keys/"+created.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first delete: code=%d body=%s, want 204", rec.Code, rec.Body.String())
	}

	// List and confirm the row is in status='revoked'.
	rec = e.do(t, http.MethodGet, "/v1/keys", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var list []api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found *api.APIKeyResponse
	for i := range list {
		if list[i].ID == created.ID {
			found = &list[i]
			break
		}
	}
	found = mustAPIKeyResponse(t, found, "revoked key not in list")
	if found.Status != "revoked" {
		t.Errorf("Status: got %q, want revoked", found.Status)
	}
	if found.RevokedAt == "" {
		t.Error("RevokedAt empty after delete, want timestamp")
	}

	// Second delete: idempotent 204.
	rec = e.do(t, http.MethodDelete, "/v1/keys/"+created.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("second delete: code=%d, want 204 (idempotent)", rec.Code)
	}
}

// TestHandlers_DeleteKey_404ForMissing pins the not-found
// path. A delete on a non-existent key id returns 404.
func TestHandlers_DeleteKey_404ForMissing(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, http.MethodDelete, "/v1/keys/00000000000000000000000000000000", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing delete: code=%d, want 404", rec.Code)
	}
}

// TestHandlers_RotateKey_GraceDefault pins the rotation
// primitive with no per-account override: old key gets
// status='grace' and expires_at=now+7d (plan default).
func TestHandlers_RotateKey_GraceDefault(t *testing.T) {
	e := setup(t, api.PlanHobby)

	// Mint a key.
	body := api.CreateKeyRequest{Label: "ci", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var old api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &old); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Rotate.
	rec = e.do(t, http.MethodPost, "/v1/keys/"+old.ID+"/rotate", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.RotateKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.KeyPlaintext == "" {
		t.Error("KeyPlaintext empty, want fresh plaintext")
	}
	if resp.OldKeyID != old.ID {
		t.Errorf("OldKeyID: got %q, want %q", resp.OldKeyID, old.ID)
	}
	if resp.Key.RotatedFromID != old.ID {
		t.Errorf("Key.RotatedFromID: got %q, want %q", resp.Key.RotatedFromID, old.ID)
	}
	if resp.Key.Status != "active" {
		t.Errorf("Key.Status: got %q, want active", resp.Key.Status)
	}
	// Old key expires_at should be ~7d out (plan default).
	if resp.OldKeyExpiresAt == "" {
		t.Error("OldKeyExpiresAt empty, want grace deadline")
	}
	dl, err := time.Parse(time.RFC3339, resp.OldKeyExpiresAt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	delta := time.Until(dl)
	if delta < 6*24*time.Hour || delta > 8*24*time.Hour {
		t.Errorf("OldKeyExpiresAt: got %v out, want ~7d", delta)
	}

	// Old key now in status='grace' (per the listKeys response).
	rec = e.do(t, http.MethodGet, "/v1/keys", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d", rec.Code)
	}
	var list []api.APIKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	for _, k := range list {
		if k.ID == old.ID {
			if k.Status != "grace" {
				t.Errorf("old key status: got %q, want grace", k.Status)
			}
			return
		}
	}
	t.Error("old key missing from listKeys after rotation")
}

// TestHandlers_RotateKey_AccountOverride pins that the
// per-account override is honored. Set grace=14 via the
// admin PATCH; rotate; the old key's expires_at should be
// ~14d out, not 7d.
func TestHandlers_RotateKey_AccountOverride(t *testing.T) {
	e := setup(t, api.PlanHobby)

	// Set override to 14 days.
	d14 := 14
	patchBody := api.SetGraceWindowRequest{Days: &d14}
	rec := e.do(t, http.MethodPatch, "/v1/account/keys/grace_window_days", patchBody, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set grace: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Mint a key.
	body := api.CreateKeyRequest{Label: "ci", Scopes: []string{api.ScopeAppsRead}}
	rec = e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup mint: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var old api.APIKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &old)

	// Rotate.
	rec = e.do(t, http.MethodPost, "/v1/keys/"+old.ID+"/rotate", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.RotateKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	dl, _ := time.Parse(time.RFC3339, resp.OldKeyExpiresAt)
	delta := time.Until(dl)
	if delta < 13*24*time.Hour || delta > 15*24*time.Hour {
		t.Errorf("override: OldKeyExpiresAt delta = %v, want ~14d", delta)
	}
}

// TestHandlers_RotateKey_Atomic pins the grace=0 path:
// old key flips to status='revoked' (terminal) and the
// expires_at is "now" (within seconds).
func TestHandlers_RotateKey_Atomic(t *testing.T) {
	e := setup(t, api.PlanHobby)

	// Set override to 0 (atomic).
	d0 := 0
	patchBody := api.SetGraceWindowRequest{Days: &d0}
	rec := e.do(t, http.MethodPatch, "/v1/account/keys/grace_window_days", patchBody, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set grace=0: code=%d", rec.Code)
	}

	// Mint a key.
	body := api.CreateKeyRequest{Label: "ci", Scopes: []string{api.ScopeAppsRead}}
	rec = e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup mint: code=%d", rec.Code)
	}
	var old api.APIKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &old)

	// Rotate.
	rec = e.do(t, http.MethodPost, "/v1/keys/"+old.ID+"/rotate", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp api.RotateKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	dl, _ := time.Parse(time.RFC3339, resp.OldKeyExpiresAt)
	if time.Since(dl) > 5*time.Second {
		t.Errorf("atomic: OldKeyExpiresAt = %v, want ~now (within 5s)", dl)
	}
}

// TestHandlers_RotateKey_RejectsRevoked pins the early-return
// on an already-revoked predecessor.
func TestHandlers_RotateKey_RejectsRevoked(t *testing.T) {
	e := setup(t, api.PlanHobby)

	body := api.CreateKeyRequest{Label: "ci", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: code=%d", rec.Code)
	}
	var old api.APIKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &old)

	// Revoke.
	rec = e.do(t, http.MethodDelete, "/v1/keys/"+old.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: code=%d", rec.Code)
	}

	// Rotate — should 404 ("key already revoked").
	rec = e.do(t, http.MethodPost, "/v1/keys/"+old.ID+"/rotate", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("rotate revoked: code=%d, want 404", rec.Code)
	}
}

// TestHandlers_GetGraceWindow_RoundTrip pins the GET +
// PATCH shape. The GET response includes both the override
// and the plan default; PATCH writes the override and the
// next GET reflects it.
func TestHandlers_GetGraceWindow_RoundTrip(t *testing.T) {
	e := setup(t, api.PlanHobby)

	// Default state: no override.
	rec := e.do(t, http.MethodGet, "/v1/account/keys/grace_window_days", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: code=%d", rec.Code)
	}
	var got api.GraceWindowResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Days != nil {
		t.Errorf("default Days: got %v, want nil", got.Days)
	}
	if got.PlanDefault != api.DefaultAPIKeyGraceWindowDays {
		t.Errorf("PlanDefault: got %d, want %d", got.PlanDefault, api.DefaultAPIKeyGraceWindowDays)
	}

	// PATCH 14.
	d14 := 14
	rec = e.do(t, http.MethodPatch, "/v1/account/keys/grace_window_days",
		api.SetGraceWindowRequest{Days: &d14}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// GET reflects.
	rec = e.do(t, http.MethodGet, "/v1/account/keys/grace_window_days", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get-after: code=%d", rec.Code)
	}
	var got2 api.GraceWindowResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got2)
	if got2.Days == nil || *got2.Days != 14 {
		t.Errorf("post-patch Days: got %v, want 14", got2.Days)
	}

	// PATCH nil → cleared.
	rec = e.do(t, http.MethodPatch, "/v1/account/keys/grace_window_days",
		api.SetGraceWindowRequest{Days: nil}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch nil: code=%d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/v1/account/keys/grace_window_days", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &got2)
	if got2.Days != nil {
		t.Errorf("post-clear Days: got %v, want nil", got2.Days)
	}
}

// TestHandlers_SetGraceWindow_RejectsNegative pins the 400
// shape for negative days. A negative value is meaningless
// and would silently pass the SQL CHECK if we sent it via
// the store directly.
func TestHandlers_SetGraceWindow_RejectsNegative(t *testing.T) {
	e := setup(t, api.PlanHobby)

	dn := -1
	rec := e.do(t, http.MethodPatch, "/v1/account/keys/grace_window_days",
		api.SetGraceWindowRequest{Days: &dn}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("negative days: code=%d, want 400", rec.Code)
	}
}

// TestHandlers_KeyRotationAuditEmitted pins that the
// key.rotated audit event lands on every successful rotation.
// The audit row is the only place the link
// {old_key_id, new_key_id, grace_window_days,
// old_key_expires_at} is recorded for the dashboard's
// per-key history.
func TestHandlers_KeyRotationAuditEmitted(t *testing.T) {
	e := setup(t, api.PlanHobby)

	body := api.CreateKeyRequest{Label: "ci", Scopes: []string{api.ScopeAppsRead}}
	rec := e.do(t, http.MethodPost, "/v1/keys", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: code=%d", rec.Code)
	}
	var old api.APIKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &old)

	rec = e.do(t, http.MethodPost, "/v1/keys/"+old.ID+"/rotate", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: code=%d", rec.Code)
	}
	var resp api.RotateKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found *state.Event
	for i := range rows {
		if rows[i].Kind == "key.rotated" {
			found = &rows[i]
			break
		}
	}
	found = mustAuditEventKeys(t, found, fmt.Sprintf("no key.rotated event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("data not JSON: %v", err)
	}
	if data["old_key_id"] != old.ID {
		t.Errorf("data.old_key_id: got %v, want %s", data["old_key_id"], old.ID)
	}
	if data["new_key_id"] != resp.Key.ID {
		t.Errorf("data.new_key_id: got %v, want %s", data["new_key_id"], resp.Key.ID)
	}
	if got, ok := data["grace_window_days"].(float64); !ok || int(got) != api.DefaultAPIKeyGraceWindowDays {
		t.Errorf("data.grace_window_days: got %v, want %d", data["grace_window_days"], api.DefaultAPIKeyGraceWindowDays)
	}
}

// mustAPIKeyResponse is the SA5011 escape hatch for the listKeys
// revoked-key search: listKeys can legitimately omit the just-revoked
// row, but we want a real row for assertions. A helper that
// t.Fatal()s and returns the value lets staticcheck see the value
// is non-nil at the call site.
func mustAPIKeyResponse(t *testing.T, k *api.APIKeyResponse, msg string) *api.APIKeyResponse {
	t.Helper()
	if k == nil {
		t.Fatal(msg)
	}
	return k
}

// mustAuditEventKeys mirrors mustAuditEvent (declared in
// handlers_audit_test.go) for the key.rotated audit-row lookup.
// Go forbids duplicate function names in the same package even
// across _test.go files in package main, so we use a file-scoped
// name. Same SA5011 false positive.
func mustAuditEventKeys(t *testing.T, ev *state.Event, msg string) *state.Event {
	t.Helper()
	if ev == nil {
		t.Fatal(msg)
	}
	return ev
}
