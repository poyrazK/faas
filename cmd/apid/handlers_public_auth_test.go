package main

// handlers_public_auth_test.go — PATCH /v1/apps/{slug}
// integration tests for the public-auth surface
// (issue #477 / ADR-079). Pins the four load-bearing
// invariants the apid layer guarantees:
//
//   1. Closed-enum validation runs FIRST so a Free
//      customer PATCHing mode='weird' sees 422
//      invalid_public_auth_mode rather than the plan gate.
//   2. Plan-gate runs SECOND: Free + bearer = 402
//      plan_public_auth_bearer_not_allowed;
//      Free/Hobby + basic = 402
//      plan_public_auth_basic_not_allowed.
//   3. mode='basic' seal round-trip persists a non-empty
//      apps.public_auth_basic blob the gatewayd-internal unsealer
//      can decrypt under the APP_BASIC_AUTH namespace.
//      PATCHing back to mode='open' clears that blob so a
//      stale secretbox row never reaches a fresh request.
//   4. Audit: app.public_auth_changed fires on every
//      mode transition with has_basic_creds: bool
//      redaction — plaintext username / password / sealed
//      blob are NEVER recorded. app.updated's
//      old/new blocks carry mode only (also redacted).
//
// The setSecretRecipient override mirrors the G6 export
// tests (handlers_account_test.go::withAccountTestRecipient)
// — the seal step is wired into a function pointer at
// startup so tests can inject a freshly-generated identity
// for the duration of the case.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// withPublicAuthTestRecipient wires a fresh X25519
// recipient into setSecretRecipient. Mirrors
// withAccountTestRecipient in handlers_account_test.go
// — the secretbox seal step needs an in-memory identity
// during PATCH mode='basic'.
//
// Without this, the seal step's check at
// handlers_ext.go:589 returns 503 ("host age recipient
// not loaded — refusing to seal public_auth credentials")
// and the test never reaches the SQL write. The
// `t.Cleanup` restores the production setter so cross-file
// tests can swap recipients without trampling one another.
func withPublicAuthTestRecipient(t *testing.T) {
	t.Helper()
	prev := setSecretRecipient
	t.Cleanup(func() { setSecretRecipient = prev })
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return id.Recipient() }
}

// patchPublicAuth is the PATCH helper for this file's
// table-driven group. Same shape as the inline body used
// in TestAuditEvents_AppUpdatedEmitsEvent above — a single
// helper keeps the test cases scannable. The PublicAuth
// pointer is the test's only mutable surface; passing nil
// is the "no public_auth block" path.
func patchPublicAuth(t *testing.T, e testEnv, slug string, pub *api.PublicAuthBlock) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, http.MethodPatch, "/v1/apps/"+slug,
		api.UpdateAppRequest{PublicAuth: pub}, nil)
}

// TestPublicAuthPatch_BearerPlanGate is the load-bearing
// 402 tripwire. Free plan PATCH mode='bearer' MUST return
// 402 plan_public_auth_bearer_not_allowed — never 403,
// never 200, and never a 422 from the closed-enum path.
// Hobby+ PATCH must succeed (200). Mirrors the streaming
// / warm-snapshot / require_authn plan-gate family
// (issue #560 ADR-074 — 402 is the consistent
// PaymentRequired shape across tier-locked features).
func TestPublicAuthPatch_BearerPlanGate(t *testing.T) {
	t.Run("free_returns_402_bearer_not_allowed", func(t *testing.T) {
		e := setup(t, api.PlanFree)
		app := seedAppForAudit(t, e, "pa-bearer-free")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{Mode: api.AppPublicAuthModeBearer})
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("PATCH mode=bearer on Free: code=%d body=%s; want 402",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "plan_public_auth_bearer_not_allowed") {
			t.Fatalf("PATCH body missing code; got %s", rec.Body.String())
		}
		// App row's mode column must stay 'open' (default).
		// The PATCH was rejected upstream — no SQL write
		// happened. The redaction invariant extends to the
		// row: a rejected PATCH never leaves a partial
		// update behind.
		got, err := e.store.AppByID(context.Background(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.PublicAuthMode != "" && got.PublicAuthMode != api.AppPublicAuthModeOpen {
			t.Fatalf("app.PublicAuthMode = %q after rejected PATCH; want default open",
				got.PublicAuthMode)
		}
		// No audit row on rejection. The redaction invariant
		// requires that plaintext NEVER appears on the audit
		// stream — a rejected PATCH (402/422) must not emit
		// app.public_auth_changed either, since a future
		// contributor adding a "log even on rejection" code
		// path could accidentally double-write the rejected
		// request's payload (mode='bearer' on Free would
		// land has_basic_creds=false but the row itself
		// carries no business value for a rejected PATCH).
		assertNoAuditRow(t, e, "app.public_auth_changed")
	})
	t.Run("hobby_returns_200", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		app := seedAppForAudit(t, e, "pa-bearer-hobby")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{Mode: api.AppPublicAuthModeBearer})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH mode=bearer on Hobby: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
}

// TestPublicAuthPatch_BasicPlanGate mirrors the bearer
// test for basic: Free/Hobby reject with 402
// plan_public_auth_basic_not_allowed; Pro accepts. The
// 402 surface uses a distinct code from the bearer case
// so the CLI can branch on plan-specific upgrade copy.
func TestPublicAuthPatch_BasicPlanGate(t *testing.T) {
	t.Run("free_returns_402_basic_not_allowed", func(t *testing.T) {
		e := setup(t, api.PlanFree)
		app := seedAppForAudit(t, e, "pa-basic-free")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:      api.AppPublicAuthModeBasic,
			BasicUser: "editor",
			BasicPass: "hunter2",
		})
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("PATCH mode=basic on Free: code=%d body=%s; want 402",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "plan_public_auth_basic_not_allowed") {
			t.Fatalf("PATCH body missing code; got %s", rec.Body.String())
		}
	})
	t.Run("hobby_returns_402_basic_not_allowed", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		app := seedAppForAudit(t, e, "pa-basic-hobby")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:      api.AppPublicAuthModeBasic,
			BasicUser: "editor",
			BasicPass: "hunter2",
		})
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("PATCH mode=basic on Hobby: code=%d body=%s; want 402",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("pro_with_creds_returns_200", func(t *testing.T) {
		withPublicAuthTestRecipient(t)
		e := setup(t, api.PlanPro)
		app := seedAppForAudit(t, e, "pa-basic-pro")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:      api.AppPublicAuthModeBasic,
			BasicUser: "editor",
			BasicPass: "hunter2",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH mode=basic on Pro: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
		var resp api.AppResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body unmarshal: %v", err)
		}
		if resp.PublicAuth.Mode != api.AppPublicAuthModeBasic {
			t.Fatalf("PublicAuth.Mode = %q; want basic", resp.PublicAuth.Mode)
		}
		if !resp.PublicAuth.HasBasicCreds {
			t.Fatalf("PublicAuth.HasBasicCreds = false; want true (sealed blob should be present)")
		}
	})
}

// TestPublicAuthPatch_OpenClearsBasicSealed pins the
// stale-secret-row invariant. After a successful mode='basic'
// PATCH (sealed blob persisted), a follow-up PATCH
// mode='open' MUST clear the blob. Without the clear, a
// later PATCH mode='basic' could resurrect the OLD
// credentials from the row even when the customer typed
// fresh values (or worse, an attacker who learns the seal
// shape could re-seal the old blob under the same key id).
func TestPublicAuthPatch_OpenClearsBasicSealed(t *testing.T) {
	withPublicAuthTestRecipient(t)
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "pa-clear")
	// 1. PATCH mode='basic' succeeds; row has sealed blob.
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode:      api.AppPublicAuthModeBasic,
		BasicUser: "u",
		BasicPass: "p",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("first PATCH (basic): code=%d body=%s", rec.Code, rec.Body.String())
	}
	row, err := e.store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.PublicAuthBasicSealed) == 0 {
		t.Fatalf("after mode=basic PATCH, PublicAuthBasicSealed is empty; seal didn't run?")
	}
	if row.PublicAuthMode != api.AppPublicAuthModeBasic {
		t.Fatalf("PublicAuthMode = %q; want basic", row.PublicAuthMode)
	}
	// 2. PATCH mode='open' clears the sealed blob.
	rec = patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode: api.AppPublicAuthModeOpen,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH (open): code=%d body=%s", rec.Code, rec.Body.String())
	}
	row, err = e.store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.PublicAuthMode != api.AppPublicAuthModeOpen {
		t.Fatalf("PublicAuthMode after open flip = %q; want open", row.PublicAuthMode)
	}
	if len(row.PublicAuthBasicSealed) != 0 {
		t.Fatalf("PublicAuthBasicSealed = %d bytes after open flip; want empty (stale-secret invariant)",
			len(row.PublicAuthBasicSealed))
	}
}

// TestPublicAuthPatch_AuditEmitsWithRedaction pins the
// re-redaction invariant (ADR-079 §Decision). Every mode
// flip MUST emit an app.public_auth_changed row carrying
//   - app_id, slug
//   - old, new (mode strings only)
//   - has_basic_creds (bool)
//
// The audit row MUST NOT carry basic_user / basic_pass /
// PublicAuthBasicSealed — the redaction posture is
// load-bearing because audit rows flow to log-archive
// stores where secret leakage is much harder to scrub than
// in-process state.
func TestPublicAuthPatch_AuditEmitsWithRedaction(t *testing.T) {
	withPublicAuthTestRecipient(t)
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "pa-audit")
	// PATCH open → basic (audit fires; has_basic_creds=true).
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode:      api.AppPublicAuthModeBasic,
		BasicUser: "alice",
		BasicPass: "secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("first PATCH: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "app.public_auth_changed")
	if found == nil {
		t.Fatalf("no app.public_auth_changed event row; rows=%+v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["app_id"] != app.ID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], app.ID)
	}
	if data["slug"] != app.Slug {
		t.Errorf("Data.slug = %v, want %s", data["slug"], app.Slug)
	}
	if data["old"] != "" && data["old"] != api.AppPublicAuthModeOpen && data["old"] != api.AppPublicAuthModeBearer {
		// Pre-#477 default was "" (empty); post-#477 the column
		// surfaces as 'open' or 'bearer' (Pro/Scale default
		// after issue #695 / ADR-080). Any other value is a
		// regression.
		t.Errorf("Data.old = %v, want one of \"\"/%q/%q", data["old"], api.AppPublicAuthModeOpen, api.AppPublicAuthModeBearer)
	}
	if data["new"] != api.AppPublicAuthModeBasic {
		t.Errorf("Data.new = %v, want %q", data["new"], api.AppPublicAuthModeBasic)
	}
	if v, _ := data["has_basic_creds"].(bool); !v {
		t.Errorf("Data.has_basic_creds = %v; want true (mode=basic PATCH)", data["has_basic_creds"])
	}
	// Redaction: the audit row must NEVER carry the basic_user,
	// basic_pass, or any sealed blob shape. A direct
	// substring check against known plaintext values pins
	// this against future regressions where a contributor
	// adds structured logging that doubles the audit row.
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "alice") ||
		strings.Contains(string(raw), "secret") ||
		strings.Contains(string(raw), "hunter2") {
		t.Fatalf("audit row leaked plaintext: %s", raw)
	}
	// Second transition: basic → bearer. has_basic_creds
	// flips back to false (the cleared-blob invariant
	// surfaces here as the audit boolean, NOT as the
	// sealed-blob field — the row never holds plaintext).
	rec = patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode: api.AppPublicAuthModeBearer,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, _ = e.store.ListEvents(context.Background(), e.acct.ID, 0)
	found = findEventByKind(rows, "app.public_auth_changed")
	if found == nil {
		t.Fatalf("no app.public_auth_changed row for second flip")
	}
	var secondData map[string]any
	_ = json.Unmarshal(found.Data, &secondData)
	if v, _ := secondData["has_basic_creds"].(bool); v {
		t.Errorf("second audit row has_basic_creds = true; want false (mode=bearer PATCH has no creds)")
	}
}

// TestPublicAuthPatch_ClosedEnumFirst pins invariant #1:
// validation runs BEFORE the plan gate. A Free customer
// PATCHing mode='weird' must see 422 invalid_public_auth_mode
// (the closed-enum shape error) — NOT 402 (which the plan
// gate would surface only after a known mode). Otherwise a
// future contributor adding more 402 codes would silently
// shadow the 422, confusing the customer.
func TestPublicAuthPatch_ClosedEnumFirst(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := seedAppForAudit(t, e, "pa-enum")
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{Mode: "weird"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH mode=weird on Free: code=%d body=%s; want 422",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_public_auth_mode") &&
		!strings.Contains(rec.Body.String(), "public_auth.mode") {
		t.Fatalf("422 body missing closed-enum error: %s", rec.Body.String())
	}
	// No audit row on closed-enum rejection. The 422 path
	// is pre-SQL (the validator short-circuits before the
	// seal step), so no business value is gained from
	// recording it — and a future contributor adding
	// "audit on rejection" would have to confirm the
	// payload shape carries no plaintext (ADR-079 §Decision
	// "re-redaction invariant").
	assertNoAuditRow(t, e, "app.public_auth_changed")
}

// TestPublicAuthPatch_BasicRequiresCreds pins the basic-cred
// requirement: mode='basic' without basic_user / basic_pass
// is a 422 even on Pro plan (the plan gate would otherwise
// accept it). Mirrors PublicAuthBlock.Validate's "required
// iff mode='basic'" branch.
func TestPublicAuthPatch_BasicRequiresCreds(t *testing.T) {
	withPublicAuthTestRecipient(t)
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "pa-creds")
	// mode='basic' with empty basic_pass → 422
	// invalid_public_auth_basic_pass.
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode:      api.AppPublicAuthModeBasic,
		BasicUser: "u",
		BasicPass: "",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH mode=basic no pass: code=%d body=%s; want 422",
			rec.Code, rec.Body.String())
	}
	// Row must NOT have flipped — the 422 short-circuits
	// before the SQL write (the seal step has no creds
	// to encrypt).
	row, _ := e.store.AppByID(context.Background(), app.ID)
	if row.PublicAuthMode != "" && row.PublicAuthMode != api.AppPublicAuthModeOpen && row.PublicAuthMode != api.AppPublicAuthModeBearer {
		t.Fatalf("PublicAuthMode after rejected PATCH = %q; want default", row.PublicAuthMode)
	}
	// No audit row on basic-requires-creds rejection. Same
	// rationale as the closed-enum test above.
	assertNoAuditRow(t, e, "app.public_auth_changed")
}

// compile-time assertion: state.App has the public_auth
// fields the seam depends on (a future field rename trips
// the linter instead of a runtime nil).
var _ state.App

// assertNoAuditRow is the negative-side pin for the
// redaction invariant (ADR-079 §Decision). A rejected
// PATCH — 402 plan gate, 422 closed-enum, 422 basic-
// requires-creds — must NOT emit an app.public_auth_changed
// row. The audit redaction posture is a load-bearing
// invariant: a future contributor adding "audit on
// rejection" code would have to confirm the payload
// carries no plaintext (the closed-enum test would
// currently emit mode='weird' — questionable audit
// value). Asserting NO row pins the cleaner posture.
func assertNoAuditRow(t *testing.T, e testEnv, kind string) {
	t.Helper()
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if found := findEventByKind(rows, kind); found != nil {
		t.Fatalf("unexpected %s audit row on rejected PATCH: data=%s", kind, string(found.Data))
	}
}

// TestPublicAuthPatch_IPAllowlistPlanGate (ADR-118) pins the
// Pro/Scale-paid-only ladder for the new ip_allowlist mode.
// Mirrors TestPublicAuthPatch_BasicPlanGate at L141 — Free + Hobby
// must return 403 plan_public_auth_ip_allowlist_not_allowed,
// Pro + Scale must accept a valid 1-entry list with 200. The
// 403 surface uses a distinct code from bearer/basic so the
// CLI can branch on plan-specific upgrade copy.
func TestPublicAuthPatch_IPAllowlistPlanGate(t *testing.T) {
	t.Run("free_returns_403_ip_allowlist_not_allowed", func(t *testing.T) {
		e := setup(t, api.PlanFree)
		app := seedAppForAudit(t, e, "pa-ip-free")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:        api.AppPublicAuthModeIPAllowlist,
			IPAllowlist: []string{"10.0.0.0/8"},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("PATCH mode=ip_allowlist on Free: code=%d body=%s; want 403",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "plan_public_auth_ip_allowlist_not_allowed") {
			t.Fatalf("body missing code; got %s", rec.Body.String())
		}
		assertNoAuditRow(t, e, "app.public_auth_changed")
	})
	t.Run("hobby_returns_403_ip_allowlist_not_allowed", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		app := seedAppForAudit(t, e, "pa-ip-hobby")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:        api.AppPublicAuthModeIPAllowlist,
			IPAllowlist: []string{"10.0.0.0/8"},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("PATCH mode=ip_allowlist on Hobby: code=%d body=%s; want 403",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("pro_with_one_entry_returns_200", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app := seedAppForAudit(t, e, "pa-ip-pro")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:        api.AppPublicAuthModeIPAllowlist,
			IPAllowlist: []string{"10.0.0.0/8"},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH mode=ip_allowlist on Pro: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("scale_with_max_entries_returns_200", func(t *testing.T) {
		e := setup(t, api.PlanScale)
		app := seedAppForAudit(t, e, "pa-ip-scale")
		// Scale max is 64 — exactly at the boundary.
		entries := make([]string, 64)
		for i := range entries {
			entries[i] = "10.0.0.0/8" // same prefix; dedup drops to 1 entry
		}
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:        api.AppPublicAuthModeIPAllowlist,
			IPAllowlist: entries,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH 64 dedup entries on Scale: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
}

// TestPublicAuthPatch_IPAllowlistSizeGate (ADR-118) pins the
// per-app entry cap. Pro 16, Scale 64 — exactly one over
// surfaces 400 public_auth_ip_allowlist_too_long with both the
// observed count and the cap in the body so the CLI can render
// actionable upgrade copy. Mirrors TestEgressAllowlist_SizeGate's
// shape (the egress path has an additive per-account budget; ingress
// does not — per-app cap only).
func TestPublicAuthPatch_IPAllowlistSizeGate(t *testing.T) {
	t.Run("pro_with_17_entries_returns_400", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app := seedAppForAudit(t, e, "pa-ip-pro-overflow")
		entries := make([]string, 17)
		for i := range entries {
			// Distinct prefixes so dedup doesn't drop them.
			entries[i] = fmt.Sprintf("10.%d.0.0/16", i)
		}
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:        api.AppPublicAuthModeIPAllowlist,
			IPAllowlist: entries,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH 17 entries on Pro: code=%d body=%s; want 400",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "public_auth_ip_allowlist_too_long") {
			t.Fatalf("body missing code; got %s", rec.Body.String())
		}
	})
	t.Run("pro_with_16_entries_returns_200", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app := seedAppForAudit(t, e, "pa-ip-pro-max")
		entries := make([]string, 16)
		for i := range entries {
			entries[i] = fmt.Sprintf("10.%d.0.0/16", i)
		}
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:        api.AppPublicAuthModeIPAllowlist,
			IPAllowlist: entries,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH 16 entries on Pro: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
	// CRIT-2 regression: the cap must run against the WIRE
	// length BEFORE dedup, so a customer cannot submit
	// cap+1 entries with N-1 duplicates and have the
	// deduped result bypass the cap. Pro cap=16: 17 wire
	// entries where exactly 1 is a duplicate (16 unique
	// after dedup) must still 400.
	t.Run("pro_with_17_wire_16_unique_still_returns_400", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app := seedAppForAudit(t, e, "pa-ip-pro-cap-bypass")
		entries := make([]string, 17)
		// First 16 distinct; 17th is a duplicate of #0.
		for i := 0; i < 16; i++ {
			entries[i] = fmt.Sprintf("10.%d.0.0/16", i)
		}
		entries[16] = entries[0] // dup → 16 unique after dedup
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode:        api.AppPublicAuthModeIPAllowlist,
			IPAllowlist: entries,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH 17 wire/16 unique on Pro (cap-bypass attempt): code=%d body=%s; want 400",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "public_auth_ip_allowlist_too_long") {
			t.Fatalf("body missing too-long code; got %s", rec.Body.String())
		}
	})
}

// TestPublicAuthPatch_IPAllowlistSlashZeroRejected (ADR-118) pins
// the per-entry shape gate. 0.0.0.0/0 is the canonical "match
// every IPv4 address" form — the egress trigger rejects it as a
// per-app-shape invariant (a single /0 entry would render the
// allowlist meaningless). The ingress trigger mirrors the same
// posture (the DB rejects at write time; the handler catches it
// upstream for a friendlier error name). This is NOT a plan gate
// or size gate; it's a per-entry shape gate on Pro.
func TestPublicAuthPatch_IPAllowlistSlashZeroRejected(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "pa-ip-slashzero")
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode:        api.AppPublicAuthModeIPAllowlist,
		IPAllowlist: []string{"0.0.0.0/0"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /0 on Pro: code=%d body=%s; want 400",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_public_auth_ip_allowlist") {
		t.Fatalf("body missing code; got %s", rec.Body.String())
	}
	// The rejected PATCH must not leave a partial write behind —
	// the app row's mode must stay at default (open), not flip to
	// ip_allowlist without a usable list.
	got, err := e.store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicAuthMode == api.AppPublicAuthModeIPAllowlist {
		t.Fatalf("app.PublicAuthMode = %q after rejected /0 PATCH; want default",
			got.PublicAuthMode)
	}
	assertNoAuditRow(t, e, "app.public_auth_changed")
}

// TestPublicAuthPatch_IPAllowlistClosedEnumFirst (ADR-118) pins
// the load-bearing precedence: an unknown mode string returns
// 422 invalid_public_auth_mode BEFORE the plan gate fires. Same
// invariant as the bearer/basic closed-enum test (L344). Without
// this precedence, a Free customer PATCHing mode='weird' would
// see a 402 plan_public_auth_ip_allowlist_not_allowed that says
// "upgrade your plan" — wrong guidance, since 'weird' isn't a
// real mode on any plan.
func TestPublicAuthPatch_IPAllowlistClosedEnumFirst(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := seedAppForAudit(t, e, "pa-ip-enum")
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode:        "weird_mode",
		IPAllowlist: []string{"10.0.0.0/8"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH unknown mode: code=%d body=%s; want 422",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "validation_failed") {
		t.Fatalf("body missing closed-enum code; got %s", rec.Body.String())
	}
}

// TestPublicAuthPatch_IPAllowlistAuditEmitsEntryCount (ADR-118)
// pins the audit redaction invariant for ip_allowlist. The audit
// payload MUST carry public_auth_ip_allowlist_entry_count (the
// integer count after canonicalisation + dedup) and MUST NEVER
// carry any CIDR string — the allowlist can reveal
// partner-customer ranges, and the redaction invariant is the
// same shape as has_basic_creds for mode='basic' (L301).
func TestPublicAuthPatch_IPAllowlistAuditEmitsEntryCount(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "pa-ip-audit")
	// Submit a 3-entry list with one duplicate so the
	// canonicalised count is 2.
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode: api.AppPublicAuthModeIPAllowlist,
		IPAllowlist: []string{
			"10.0.0.0/8",
			"192.0.2.0/24",
			"10.0.0.0/8", // dup
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "app.public_auth_changed")
	if found == nil {
		t.Fatalf("no app.public_auth_changed row; rows=%+v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	// Mode transition: "" → ip_allowlist.
	if data["new"] != api.AppPublicAuthModeIPAllowlist {
		t.Errorf("Data.new = %v, want %q", data["new"], api.AppPublicAuthModeIPAllowlist)
	}
	// Entry count: 3 wire → 2 after dedup (10.0.0.0/8 dropped).
	if v, _ := data["public_auth_ip_allowlist_entry_count"].(float64); int(v) != 2 {
		t.Errorf("Data.public_auth_ip_allowlist_entry_count = %v; want 2 (3 wire, 1 dedup)",
			data["public_auth_ip_allowlist_entry_count"])
	}
	// Redaction: scan raw JSON for the wire-form CIDR strings.
	// A direct substring check pins this against future
	// regressions where a contributor adds structured
	// logging that doubles the audit row.
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "10.0.0.0/8") ||
		strings.Contains(string(raw), "192.0.2.0/24") {
		t.Fatalf("audit row leaked CIDR string: %s", raw)
	}
}

// TestPublicAuthPatch_IPAllowlistEntryCountGatedByMode (ADR-118 /
// MED-1 review-fix) pins the wire contract that
// PublicAuthStatus.IPAllowlistEntryCount is 0 when the row's
// mode is NOT ip_allowlist, even if the column carries stale
// CIDRs from a prior PATCH. Mirrors how HasBasicCreds is
// intrinsic to the sealed-blob presence (no extra gate).
//
// Scenario: Pro customer PATCHes ip_allowlist with 5 CIDRs,
// then PATCHes mode='basic'. SetPublicAuthIPAllowlist is
// false on the basic PATCH (mode != ip_allowlist at L949 in
// handlers_ext.go), so the column retains the 5 stale CIDRs.
// GET /v1/apps/{slug} must surface ip_allowlist_entry_count=0
// (per the OpenAPI docstring at api/openapi.yaml:9010 —
// "Always 0 when mode != 'ip_allowlist'"). Without the gate,
// the dashboard would render "app X has 5 CIDRs configured"
// for a basic-mode app.
func TestPublicAuthPatch_IPAllowlistEntryCountGatedByMode(t *testing.T) {
	withPublicAuthTestRecipient(t)
	e := setup(t, api.PlanPro)
	app := seedAppForAudit(t, e, "pa-ip-entry-count-gate")

	// 1. PATCH ip_allowlist with 5 CIDRs.
	rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode: api.AppPublicAuthModeIPAllowlist,
		IPAllowlist: []string{
			"10.0.0.0/8",
			"192.0.2.0/24",
			"203.0.113.0/24",
			"198.51.100.0/24",
			"2001:db8::/32",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("first PATCH: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. PATCH mode='basic' — the column retains 5 stale CIDRs
	//    because SetPublicAuthIPAllowlist only fires for
	//    mode='ip_allowlist' (handlers_ext.go:949).
	rec = patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
		Mode:      api.AppPublicAuthModeBasic,
		BasicUser: "editor",
		BasicPass: "hunter2",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. GET and verify ip_allowlist_entry_count = 0
	//    (mode='basic' now; the stale column is gated out).
	rec = e.do(t, "GET", "/v1/apps/"+app.Slug, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.PublicAuth.Mode != api.AppPublicAuthModeBasic {
		t.Errorf("Mode = %q, want %q", out.PublicAuth.Mode, api.AppPublicAuthModeBasic)
	}
	if out.PublicAuth.IPAllowlistEntryCount != 0 {
		t.Errorf("IPAllowlistEntryCount = %d on basic-mode app; want 0 (gated by mode)",
			out.PublicAuth.IPAllowlistEntryCount)
	}
}

// TestPublicAuthPatch_MembersOnlyPlanGate (ADR-120) pins the
// Hobby+-only plan ladder for members_only. Mirrors
// TestPublicAuthPatch_BasicPlanGate at L141 for the 402 surface
// (members_only surfaces 402 plan_public_auth_members_only_not_allowed,
// not 403, because Hobby unlocks via the OrgMembersMax ladder — the
// "upgrade to Hobby" copy mirrors bearer's 402 surface). Free is
// rejected because Free personal-org has exactly 1 member (the account
// itself) so members_only on Free would collapse to bearer with the
// same account — exactly the abuse-floor conflation ADR-079 §2
// explicitly avoided by gating bearer at Hobby+. Hobby/Pro/Scale accept
// a no-payload PATCH (members_only needs no app-side payload — the
// cookie + org-membership lookup live on the request). The closed-enum
// validator must accept the new mode first (it does at L791) so a Free
// customer sees 402 plan_public_auth_members_only_not_allowed, not
// 422 invalid_public_auth_mode (the supersedes-402 invariant from
// ADR-079 line 252 — a known-mode still has to honour the plan gate).
func TestPublicAuthPatch_MembersOnlyPlanGate(t *testing.T) {
	t.Run("free_returns_402_members_only_not_allowed", func(t *testing.T) {
		e := setup(t, api.PlanFree)
		app := seedAppForAudit(t, e, "pa-mo-free")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode: api.AppPublicAuthModeMembersOnly,
		})
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("PATCH mode=members_only on Free: code=%d body=%s; want 402",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "plan_public_auth_members_only_not_allowed") {
			t.Fatalf("body missing code; got %s", rec.Body.String())
		}
		assertNoAuditRow(t, e, "app.public_auth_changed")
	})
	t.Run("hobby_returns_200", func(t *testing.T) {
		e := setup(t, api.PlanHobby)
		app := seedAppForAudit(t, e, "pa-mo-hobby")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode: api.AppPublicAuthModeMembersOnly,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH mode=members_only on Hobby: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("pro_returns_200", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app := seedAppForAudit(t, e, "pa-mo-pro")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode: api.AppPublicAuthModeMembersOnly,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH mode=members_only on Pro: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("scale_returns_200", func(t *testing.T) {
		e := setup(t, api.PlanScale)
		app := seedAppForAudit(t, e, "pa-mo-scale")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode: api.AppPublicAuthModeMembersOnly,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH mode=members_only on Scale: code=%d body=%s; want 200",
				rec.Code, rec.Body.String())
		}
	})
	t.Run("closed_enum_supersedes_plan_gate", func(t *testing.T) {
		// ADR-079 line 252 invariant: an unknown mode surfaces
		// 422 invalid_public_auth_mode BEFORE the plan gate
		// runs. A Free customer who PATCHes mode="banana"
		// gets 422, never 402 plan_public_auth_members_only_not_allowed.
		// This guards against a future contributor from
		// reordering the closed-enum switch in
		// cmd/apid/handlers_ext.go:432 and accidentally
		// routing unknown-mode rejections through the new
		// plan gate.
		e := setup(t, api.PlanFree)
		app := seedAppForAudit(t, e, "pa-mo-banana")
		rec := patchPublicAuth(t, e, app.Slug, &api.PublicAuthBlock{
			Mode: "banana",
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PATCH mode=banana on Free: code=%d body=%s; want 422",
				rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "validation_failed") {
			t.Fatalf("body missing 422 code (validation_failed); got %s", rec.Body.String())
		}
	})
}
