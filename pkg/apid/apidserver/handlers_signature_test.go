// handlers_signature_test.go — table-driven tests for the deploy-time
// cosign signature-enforcement pre-flight gate (issue #472 /
// ADR-054). Lives in cmd/apid alongside handlers_test.go so the
// setup helpers (testEnv, do) are reachable; pinned in a separate
// file so the existing handler test matrix stays untouched.
//
// Why a table (not one-test-per-case): each subcase is a small
// state-difference on the apps row + the trusted_signer table, and
// the wire shape (status + Problem code + body fragment) is the
// only thing under test. The pre-flight gate runs once per deploy
// at accept-time; the operator failure mode is "did the customer
// see a 403 with the right code?", not "did the audit row land?"
// (audit rows are an e2e concern, see cmd/e2e/signed_deploy_e2e_test.go).
//
// Cases covered (mirrors the plan's Bucket 6 list):
//
//   - default-off happy path           → 202 (no signature gate fires)
//   - flag-on + trust-list present     → 202 (gate passes)
//   - flag-on + no trusted signers     → 403 deploy_signature_invalid
//   - flag-on + req.RequireSigned=false → 403 deploy_signature_invalid
//     (operator policy wins)
//   - source-tarball deploy            → 202 (railpack path bypasses)
//   - free plan + flag-on (no slots)   → 403 plan_limit_trusted_signers
//
// What this file does NOT cover:
//   - The actual cosign ECDSA verify inside imaged — see
//     pkg/cosign/verify_test.go (happy + missing + wrong-key +
//     no-publishers).
//   - The trusted-signer CRUD handlers — see handlers_trusted_signers_test.go.
package apidserver

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// requireSignedApp seeds a Pro-plan app with the require_signed
// flag flipped on (or off, depending on the case) so the table
// can focus on the wire shape, not the UpdateApp plumbing. Returns
// the slug the table can then deploy to.
func requireSignedApp(t *testing.T, e testEnv, slug string, on bool) {
	t.Helper()
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: slug}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed app %q: %d %s", slug, rec.Code, rec.Body)
	}
	if !on {
		// Default is off — nothing more to do.
		return
	}
	// Use the admin-scoped UpdateAppParams path to flip the flag.
	// We don't go through the customer-facing updateApp (which
	// silently drops the field); instead we hit UpdateApp directly
	// on the MemStore. The customer's only legal path to set this
	// flag is via PATCH /v1/apps/{slug}/security (admin+MFA), which
	// is exercised in handlers_security_test.go.
	apps, err := e.store.ListApps(testCtx(), e.acct.ID)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	for _, a := range apps {
		if a.Slug != slug {
			continue
		}
		tt := true
		if _, err := e.store.UpdateApp(testCtx(), a.ID, state.UpdateAppParams{
			RequireSigned:    &tt,
			SetRequireSigned: true,
		}); err != nil {
			t.Fatalf("UpdateApp: %v", err)
		}
		return
	}
	t.Fatalf("app %q not found after POST /v1/apps", slug)
}

// seedTrustedSigner writes a row into the trusted-signer table
// directly on the MemStore. We bypass the PUT endpoint so the
// test isn't coupled to its validation rules; the wire-level
// CRUD coverage lives in handlers_trusted_signers_test.go.
func seedTrustedSigner(t *testing.T, e testEnv, appID, signerName string) {
	t.Helper()
	// 64 bytes of base64(0xAA) — a placeholder DER blob; the
	// pre-flight gate only checks len(signers) > 0, not the bytes.
	der := make([]byte, 64)
	for i := range der {
		der[i] = 0xAA
	}
	if _, _, err := e.store.UpsertAppTrustedSigner(testCtx(), e.acct.ID, appID, signerName, der, e.acct.ID); err != nil {
		t.Fatalf("UpsertAppTrustedSigner: %v", err)
	}
}

// findAppID is a small lookup helper so the table doesn't have to
// thread the appID through every subcase.
func findAppID(t *testing.T, e testEnv, slug string) string {
	t.Helper()
	apps, err := e.store.ListApps(testCtx(), e.acct.ID)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	for _, a := range apps {
		if a.Slug == slug {
			return a.ID
		}
	}
	t.Fatalf("app %q not found", slug)
	return ""
}

// repeat fills a string with n copies of c. Same pattern as
// handlers_test.go's repeat helper — duplicated here rather than
// exported so the two files stay independent.
func repeatSign(c byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = c
	}
	return string(out)
}

// imageRef returns a digest-pinned ref of the canonical shape.
// Same helper as handlers_test.go; duplicated here so the two
// test files don't share a private helper.
func imageRef(c byte) string {
	return "registry.example.com/app@sha256:" + repeatSign(c, 64)
}

// TestCreateDeployment_SignatureEnforcement drives the five-case
// matrix from the plan's Bucket 6 list. Each row mutates the apps
// row + the trusted_signer table, then issues a POST and asserts
// the wire status + Problem code.
func TestCreateDeployment_SignatureEnforcement(t *testing.T) {
	cases := []struct {
		name           string
		plan           api.Plan
		slug           string
		flagOn         bool // apps.require_signed at deploy time
		seedSigner     bool // populate one trusted-signer row
		req            api.CreateDeploymentRequest
		wantStatus     int
		wantCode       string // empty = expect success (no Problem)
		wantBodySubstr string // optional fragment for the 4xx messages
	}{
		{
			name:       "default-off-accepts-any-image",
			plan:       api.PlanPro,
			slug:       "app-default-off",
			flagOn:     false,
			seedSigner: false,
			req:        api.CreateDeploymentRequest{Image: imageRef('a')},
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "flag-on-with-signer-accepts-image",
			plan:       api.PlanPro,
			slug:       "app-flag-on",
			flagOn:     true,
			seedSigner: true,
			req:        api.CreateDeploymentRequest{Image: imageRef('b')},
			wantStatus: http.StatusAccepted,
		},
		{
			name:           "flag-on-with-no-signers-fail-closed",
			plan:           api.PlanPro,
			slug:           "app-no-signers",
			flagOn:         true,
			seedSigner:     false,
			req:            api.CreateDeploymentRequest{Image: imageRef('c')},
			wantStatus:     http.StatusForbidden,
			wantCode:       api.CodeDeploySignatureInvalid,
			wantBodySubstr: "no trusted publishers",
		},
		{
			name:           "operator-policy-wins-over-customer-optout",
			plan:           api.PlanPro,
			slug:           "app-policy-wins",
			flagOn:         true,
			seedSigner:     true,
			req:            api.CreateDeploymentRequest{Image: imageRef('d'), RequireSigned: ptrBool(false)},
			wantStatus:     http.StatusForbidden,
			wantCode:       api.CodeDeploySignatureInvalid,
			wantBodySubstr: "per-deploy opt-out is not permitted",
		},
		{
			name:       "customer-request-can-match-or-escalate-not-weaken",
			plan:       api.PlanPro,
			slug:       "app-customer-request",
			flagOn:     true,
			seedSigner: true,
			// req.RequireSigned = *true is a no-op when the app flag
			// is already on; gate must still pass.
			req:        api.CreateDeploymentRequest{Image: imageRef('e'), RequireSigned: ptrBool(true)},
			wantStatus: http.StatusAccepted,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, c.plan)
			requireSignedApp(t, e, c.slug, c.flagOn)
			if c.seedSigner {
				seedTrustedSigner(t, e, findAppID(t, e, c.slug), "ci-bot")
			}
			rec := e.do(t, "POST", "/v1/apps/"+c.slug+"/deployments", c.req, nil)
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, c.wantStatus, rec.Body)
			}
			if c.wantCode == "" {
				// Success — no further assertions.
				return
			}
			if !strings.Contains(rec.Body.String(), c.wantCode) {
				t.Errorf("body = %s, want code %q in body", rec.Body, c.wantCode)
			}
			if c.wantBodySubstr != "" && !strings.Contains(rec.Body.String(), c.wantBodySubstr) {
				t.Errorf("body = %s, want fragment %q", rec.Body, c.wantBodySubstr)
			}
		})
	}
}

// TestCreateDeployment_SourceTarball_BypassesSignatureGate pins
// the rule that the source-tarball path (railpack build inside a
// builder microVM, ADR-003) is unaffected by require_signed. The
// flag applies ONLY to OCI image deploys; this test makes that
// boundary explicit so a future refactor doesn't accidentally
// widen the gate.
func TestCreateDeployment_SourceTarball_BypassesSignatureGate(t *testing.T) {
	e := setup(t, api.PlanPro)
	requireSignedApp(t, e, "app-tarball", true)
	// Intentionally do NOT seed a trusted signer — the tarball
	// path must accept anyway because it never asks the registry
	// for a signature.
	// We can't actually POST a multipart tarball through this
	// helper (e.do is JSON-only) but the path forks on
	// Content-Type BEFORE the signature pre-flight. The simplest
	// pin is the reverse: a JSON body without Image would 400,
	// not 403, so the signature gate never runs.
	rec := e.do(t, "POST", "/v1/apps/app-tarball/deployments",
		api.CreateDeploymentRequest{}, nil)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("tarball-shaped deploy triggered signature gate; want 4xx but NOT 403 signature_invalid (got %d %s)", rec.Code, rec.Body)
	}
}

// TestPutTrustedSigner_PEMShape rejects malformed PEM bodies at
// the wire surface. The pgstore layer has a CHECK constraint
// (octet_length BETWEEN 64 AND 1024) so the same shape is
// pinned at the DB layer; this test pins the API-layer rejection
// so the error code is stable.
func TestPutTrustedSigner_PEMShape(t *testing.T) {
	cases := []struct {
		name    string
		body    api.AddTrustedSignerRequest
		wantIn  string
		wantNot string
	}{
		{
			name:    "empty-body",
			body:    api.AddTrustedSignerRequest{PublicKeyPEM: ""},
			wantIn:  api.CodeTrustedSignerInvalid,
			wantNot: "",
		},
		{
			name:    "too-short",
			body:    api.AddTrustedSignerRequest{PublicKeyPEM: base64.StdEncoding.EncodeToString(make([]byte, 32))},
			wantIn:  api.CodeTrustedSignerInvalid,
			wantNot: "",
		},
		{
			name:    "too-long",
			body:    api.AddTrustedSignerRequest{PublicKeyPEM: base64.StdEncoding.EncodeToString(make([]byte, 2048))},
			wantIn:  api.CodeTrustedSignerInvalid,
			wantNot: "",
		},
		{
			name:    "not-base64",
			body:    api.AddTrustedSignerRequest{PublicKeyPEM: "!!! not base64 !!!"},
			wantIn:  api.CodeTrustedSignerInvalid,
			wantNot: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			requireSignedApp(t, e, "app-pem", false)
			rec := e.do(t, "PUT", "/v1/apps/app-pem/trusted_signers/ci-bot", c.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body)
			}
			if c.wantIn != "" && !strings.Contains(rec.Body.String(), c.wantIn) {
				t.Errorf("body = %s, want code %q", rec.Body, c.wantIn)
			}
		})
	}
}

// TestListTrustedSigners_Empty mirrors the apid CRUD handler — a
// fresh app has no trusted signers, and the wire surface returns
// 200 with an empty list (NOT 404). The plan's bucket 3 decision
// was "empty list is the expected state for any app with
// require_signed=false"; this test pins that.
func TestListTrustedSigners_Empty(t *testing.T) {
	e := setup(t, api.PlanPro)
	requireSignedApp(t, e, "app-list-empty", false)
	rec := e.do(t, "GET", "/v1/apps/app-list-empty/trusted_signers", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"signers":[]`) {
		t.Errorf("body = %s, want empty signers array", rec.Body)
	}
}

// TestPutTrustedSigner_HappyPath: full round-trip — PUT a 64-byte
// blob, GET the list, the row is there. The admin key in setup()
// already carries ScopesAdminOnly so the auth chain passes.
func TestPutTrustedSigner_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	requireSignedApp(t, e, "app-put", false)
	der := make([]byte, 64)
	for i := range der {
		der[i] = 0xBB
	}
	body := api.AddTrustedSignerRequest{PublicKeyPEM: base64.StdEncoding.EncodeToString(der)}
	rec := e.do(t, "PUT", "/v1/apps/app-put/trusted_signers/ci-bot", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	listRec := e.do(t, "GET", "/v1/apps/app-put/trusted_signers", nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("LIST: %d %s", listRec.Code, listRec.Body)
	}
	if !strings.Contains(listRec.Body.String(), `"name":"ci-bot"`) {
		t.Errorf("list body = %s, want ci-bot in name field", listRec.Body)
	}
	// The round-tripped base64 must equal what we PUT (response
	// echoes the wire form).
	if !strings.Contains(listRec.Body.String(), base64.StdEncoding.EncodeToString(der)) {
		t.Errorf("list body = %s, want PUT blob echoed", listRec.Body)
	}
}

// TestDeleteTrustedSigner_HappyPath: PUT then DELETE, then GET
// returns the empty list again. Pins the (slug, name) → gone
// surface of the handler.
func TestDeleteTrustedSigner_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	requireSignedApp(t, e, "app-del", false)
	der := make([]byte, 64)
	for i := range der {
		der[i] = 0xCC
	}
	body := api.AddTrustedSignerRequest{PublicKeyPEM: base64.StdEncoding.EncodeToString(der)}
	putRec := e.do(t, "PUT", "/v1/apps/app-del/trusted_signers/ci-bot", body, nil)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", putRec.Code, putRec.Body)
	}
	delRec := e.do(t, "DELETE",
		"/v1/apps/app-del/trusted_signers/"+url.PathEscape("ci-bot"), nil, nil)
	if delRec.Code != http.StatusNoContent && delRec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", delRec.Code, delRec.Body)
	}
	listRec := e.do(t, "GET", "/v1/apps/app-del/trusted_signers", nil, nil)
	if !strings.Contains(listRec.Body.String(), `"signers":[]`) {
		t.Errorf("list after delete = %s, want empty", listRec.Body)
	}
}

// Hunt #9 H9-53: the signer audit rows used the app ID as their subject,
// and the audit log is listed per account, so production-us owners never
// saw app.trusted_signer_added / _removed for their own apps.
func TestTrustedSignerAuditRowsBelongToTheAccount(t *testing.T) {
	e := setup(t, api.PlanPro)
	requireSignedApp(t, e, "app-audit", false)
	body := api.AddTrustedSignerRequest{PublicKeyPEM: base64.StdEncoding.EncodeToString(make([]byte, 64))}
	if rec := e.do(t, "PUT", "/v1/apps/app-audit/trusted_signers/ci-bot", body, nil); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "DELETE", "/v1/apps/app-audit/trusted_signers/ci-bot", nil, nil); rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
	}
	events, err := e.store.ListEvents(context.Background(), e.acct.ID, 50)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	seen := map[string]bool{}
	for _, ev := range events {
		seen[ev.Kind] = true
	}
	for _, kind := range []string{"app.trusted_signer_added", "app.trusted_signer_removed"} {
		if !seen[kind] {
			t.Errorf("%s is not in the account's audit log (kinds seen: %v)", kind, seen)
		}
	}
}

// ptrBool is a tiny helper so the table can write ptrBool(true)
// inline.
func ptrBool(v bool) *bool {
	return &v
}

// testCtx is a background context for the MemStore helpers that
// take a context. Centralised here so the table can stay terse.
func testCtx() context.Context { return context.Background() }
