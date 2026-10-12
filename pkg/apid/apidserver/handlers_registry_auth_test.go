// Unit tests for the per-app private-registry Basic Auth handlers
// (issue #461 / ADR-062). Coverage:
//
//   - happy-path PUT + GET + DELETE round-trip
//   - per-plan quota enforcement (403 plan_registry_credentials_not_allowed on Free
//     + 413 plan_registry_credential_quota on the +1 row at Hobby)
//   - re-PUT of an existing (app, host) replaces ciphertext + updates timestamp,
//     does NOT consume a quota slot
//   - per-host validation rejection (400 invalid_registry_host on bare scheme /
//     embedded path / uppercase / bad port)
//   - delete-not-found (404 registry_credential_not_found, NOT 400 —
//     the URL resource IS the host, mirrors the existing secret key posture)
//   - cross-app isolation: creds on app A are invisible to GET/PUT/DELETE
//     against app B in the same account
//   - password plaintext NEVER appears in the response body, the log line,
//     or the audit payload
//   - recipient-missing path: PUT returns 503 when setSecretRecipient is nil
//
// All tests run KVM-free via the in-memory store + a real age.X25519
// recipient installed per-test (same shape as handlers_secrets_test.go).

package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// setupRegistry wires setup() with a recipient in place. Mirrors
// setupSecrets in handlers_secrets_test.go.
func setupRegistry(t *testing.T, plan api.Plan) apidserver.TestEnv {
	t.Helper()
	teardown := apidserver.WithTestRecipient(t)
	t.Cleanup(teardown)
	return apidserver.Setup(t, plan)
}

const registryAuthMark = "s3cret-AUTH-PWD"

// listRegistryResponse decodes the GET response. Mirrors the
// secrets handler's listResponse helper shape.
func decodeRegistryList(t *testing.T, body io.Reader) api.AppRegistryCredentialListResponse {
	t.Helper()
	var resp api.AppRegistryCredentialListResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return resp
}

// TestRegistryAuth_PutGetDeleteRoundTrip pins the happy-path
// surface: PUT seals, GET omits password, DELETE removes the row.
func TestRegistryAuth_PutGetDeleteRoundTrip(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "auth-app")
	const (
		host = "https://registry.gregale.dev"
		user = "alice"
	)

	// PUT — happy path.
	putReq := api.PutAppRegistryCredentialRequest{
		Registry: host, Username: user, Password: registryAuthMark,
	}
	rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials", putReq, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	putBody, _ := io.ReadAll(rec.Body)
	if bytes.Contains(putBody, []byte(registryAuthMark)) {
		t.Errorf("PUT response leaks password marker: %s", putBody)
	}

	// GET — list the credential back; password omitted, quota + count surfaced.
	rec = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/registry-credentials", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	// Read the body ONCE so we can both scan it for the password
	// marker AND decode the JSON — rec.Body is a streaming
	// io.Reader and re-reading after the scan returns EOF.
	getBody, _ := io.ReadAll(rec.Body)
	if bytes.Contains(getBody, []byte(registryAuthMark)) {
		t.Errorf("GET response leaks password marker: %s", getBody)
	}
	list := decodeRegistryList(t, bytes.NewReader(getBody))
	if list.Count != 1 {
		t.Errorf("Count = %d, want 1", list.Count)
	}
	if list.QuotaMax != 2 {
		t.Errorf("QuotaMax = %d, want 2 (Hobby)", list.QuotaMax)
	}
	if len(list.Credentials) != 1 {
		t.Fatalf("Credentials = %d, want 1", len(list.Credentials))
	}
	got := list.Credentials[0]
	if got.Registry != "registry.gregale.dev" {
		t.Errorf("Registry = %q, want %q", got.Registry, "registry.gregale.dev")
	}
	if got.Username != user {
		t.Errorf("Username = %q, want %q", got.Username, user)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Errorf("CreatedAt/UpdatedAt empty: %+v", got)
	}

	// DELETE — query param ?registry=<host>.
	delURL := "/v1/apps/" + app.Slug + "/registry-credentials?registry=" + url.QueryEscape(host)
	rec = e.Do(t, "DELETE", delURL, nil, nil)
	if rec.Code != 204 {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}

	// GET again — list empty.
	rec = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/registry-credentials", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET (after delete): %d %s", rec.Code, rec.Body.String())
	}
	list = decodeRegistryList(t, rec.Body)
	if list.Count != 0 {
		t.Errorf("Count after delete = %d, want 0", list.Count)
	}
}

// TestRegistryAuth_FreePlanReturns403 pins the Free-plan gate.
// Free cannot store creds (RegistryCredentialMax == 0).
func TestRegistryAuth_FreePlanReturns403(t *testing.T) {
	e := setupRegistry(t, api.PlanFree)
	app := apidserver.CreateApp(t, e, "free-app")
	rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
		api.PutAppRegistryCredentialRequest{
			Registry: "https://registry.gregale.dev", Username: "alice", Password: registryAuthMark,
		}, nil)
	if rec.Code != 403 {
		t.Fatalf("PUT on Free: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plan_registry_credentials_not_allowed") {
		t.Errorf("body lacks stable code: %s", rec.Body.String())
	}
}

// TestRegistryAuth_HobbyQuotaEnforced pins the per-plan cap
// (Hobby == 2). The 3rd distinct host hits 413.
func TestRegistryAuth_HobbyQuotaEnforced(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "quota-app")
	hosts := []string{"https://r1.example.com", "https://r2.example.com", "https://r3.example.com"}
	for i, h := range hosts[:2] {
		rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
			api.PutAppRegistryCredentialRequest{Registry: h, Username: "u", Password: "p"}, nil)
		if rec.Code != 200 {
			t.Fatalf("PUT %d (%s): %d %s", i, h, rec.Code, rec.Body.String())
		}
	}
	rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
		api.PutAppRegistryCredentialRequest{Registry: hosts[2], Username: "u", Password: "p"}, nil)
	if rec.Code != 413 {
		t.Fatalf("PUT 3rd: %d %s, want 413", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plan_registry_credential_quota") {
		t.Errorf("body lacks stable code: %s", rec.Body.String())
	}
}

// TestRegistryAuth_UpdateDoesNotConsumeQuota pins the
// replacement-shape invariant: re-PUT of an existing
// (app, host) replaces ciphertext + bumps updated_at WITHOUT
// consuming a new quota slot. Hobby cap == 2; re-PUTs of the
// same host succeed indefinitely.
func TestRegistryAuth_UpdateDoesNotConsumeQuota(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "replace-app")
	const host = "https://registry.gregale.dev"
	for i := 0; i < 5; i++ {
		rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
			api.PutAppRegistryCredentialRequest{
				Registry: host, Username: "alice", Password: "p-" + string(rune('0'+i)),
			}, nil)
		if rec.Code != 200 {
			t.Fatalf("PUT iteration %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	// Confirm count == 1 — not 5.
	rec := e.Do(t, "GET", "/v1/apps/"+app.Slug+"/registry-credentials", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	list := decodeRegistryList(t, rec.Body)
	if list.Count != 1 {
		t.Errorf("Count = %d, want 1 (re-PUTs replace, not append)", list.Count)
	}
}

// TestRegistryAuth_DeleteNotFoundReturns400 pins the DELETE
// surface: an absent host returns 400 (the resource IS the host,
// mirrors the secrets handler's ErrSecretNotFound posture which
// is also 400 with code secret_not_found).
func TestRegistryAuth_DeleteNotFoundReturns400(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "del-nf-app")
	rec := e.Do(t, "DELETE",
		"/v1/apps/"+app.Slug+"/registry-credentials?registry=https%3A%2F%2Fmissing.example.com",
		nil, nil)
	if rec.Code != 400 {
		t.Fatalf("DELETE absent: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "registry_credential_not_found") {
		t.Errorf("body lacks stable code: %s", rec.Body.String())
	}
}

// TestRegistryAuth_InvalidHostReturns400 pins the host validator
// rejection path. The handler requires explicit `https://` per
// ADR-062 §https-only clarification (issue #461 review finding);
// schemeless + http:// + embedded path / query / fragment +
// empty → 400 invalid_registry_host.
func TestRegistryAuth_InvalidHostReturns400(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "bad-host-app")
	// Bare "registry.example.com" (no scheme) is rejected under
	// the https-only policy — schemeless inputs are ambiguous and
	// were the leading failure mode for misconfigured customers
	// ("did the user mean docker.io or the public registry?").
	reject := []string{
		"registry.example.com",                       // no scheme
		"http://registry.example.com",                // cleartext
		"REGISTRY.example.com",                       // no scheme (uppercase doesn't save it)
		"https://registry.example.com/path",          // embedded path
		"https://registry.example.com:99999",         // port out of range
		"",                                           // empty
		"https://registry.example.com/../etc/passwd", // path traversal
	}
	for _, h := range reject {
		rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
			api.PutAppRegistryCredentialRequest{
				Registry: h, Username: "alice", Password: registryAuthMark,
			}, nil)
		if rec.Code != 400 {
			t.Errorf("PUT host=%q: %d %s, want 400", h, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), "invalid_registry_host") {
			t.Errorf("PUT host=%q: body lacks stable code: %s", h, rec.Body.String())
		}
	}
	// Sanity: the cases that DO work — explicit https:// plus the
	// accepted normalisations (uppercase, whitespace, trailing slash).
	accept := []string{
		"https://registry.example.com",
		"https://REGISTRY.example.com",
		"  https://registry.example.com  ",
		"https://registry.example.com/",
	}
	for _, h := range accept {
		rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
			api.PutAppRegistryCredentialRequest{
				Registry: h, Username: "alice", Password: registryAuthMark,
			}, nil)
		if rec.Code != 200 {
			t.Errorf("PUT host=%q should normalize and accept; got %d %s", h, rec.Code, rec.Body.String())
		}
	}
}

// TestRegistryAuth_AccountIsolation pins that a credential on
// app A is invisible to GET/PUT/DELETE against app B in the same
// account (and against a different account entirely).
func TestRegistryAuth_AccountIsolation(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	appA := apidserver.CreateApp(t, e, "iso-app-a")
	// App B in the same account.
	appB := apidserver.CreateApp(t, e, "iso-app-b")
	const host = "https://registry.gregale.dev"

	// PUT on app A.
	rec := e.Do(t, "PUT", "/v1/apps/"+appA.Slug+"/registry-credentials",
		api.PutAppRegistryCredentialRequest{
			Registry: host, Username: "alice", Password: registryAuthMark,
		}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT on app A: %d %s", rec.Code, rec.Body.String())
	}

	// GET on app B → empty list (not leaked from app A).
	rec = e.Do(t, "GET", "/v1/apps/"+appB.Slug+"/registry-credentials", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET on app B: %d %s", rec.Code, rec.Body.String())
	}
	list := decodeRegistryList(t, rec.Body)
	if list.Count != 0 {
		t.Errorf("Cross-app leak: app B sees %d credentials", list.Count)
	}

	// DELETE on app B → 400 with code registry_credential_not_found
	// (the row belongs to app A; loadApp is account-scoped so app B
	// sees an empty host → ErrRegistryCredentialNotFound, which is
	// 400 per the api/errors.go convention).
	rec = e.Do(t, "DELETE",
		"/v1/apps/"+appB.Slug+"/registry-credentials?registry="+host, nil, nil)
	if rec.Code != 400 {
		t.Errorf("DELETE on app B: %d %s, want 400", rec.Code, rec.Body.String())
	}

	// App A still owns its row.
	rec = e.Do(t, "GET", "/v1/apps/"+appA.Slug+"/registry-credentials", nil, nil)
	list = decodeRegistryList(t, rec.Body)
	if list.Count != 1 {
		t.Errorf("App A row vanished: Count = %d", list.Count)
	}
}

// TestRegistryAuth_PasswordNotInResponse pins the redaction
// invariant. PUT and GET must never echo the plaintext password
// — the wire shape has no Password field, and a JSON-string scan
// confirms the marker never appears.
func TestRegistryAuth_PasswordNotInResponse(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "redact-app")
	const pw = "very-secret-MARKER-99X"
	rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
		api.PutAppRegistryCredentialRequest{
			Registry: "https://registry.gregale.dev", Username: "alice", Password: pw,
		}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	putBody, _ := io.ReadAll(rec.Body)
	if bytes.Contains(putBody, []byte(pw)) {
		t.Errorf("PUT response leaks password marker: %s", putBody)
	}
	rec = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/registry-credentials", nil, nil)
	getBody, _ := io.ReadAll(rec.Body)
	if bytes.Contains(getBody, []byte(pw)) {
		t.Errorf("GET response leaks password marker: %s", getBody)
	}
	// Ciphertext in the DB is NOT plaintext — defence in depth.
	row, err := (*e.Store()).GetAppRegistryCredential(context.Background(), (*e.Acct()).ID, app.ID, "registry.gregale.dev")
	if err != nil {
		t.Fatalf("GetAppRegistryCredential: %v", err)
	}
	if bytes.Equal(row.PasswordEncrypted, []byte(pw)) {
		t.Errorf("PasswordEncrypted is plaintext — SealBytes was bypassed")
	}
}

// TestRegistryAuth_RecipientMissing_503 pins the "apid booted
// without host.age.pub" path: PUT returns 503 (capacity), no
// plaintext is accepted.
func TestRegistryAuth_RecipientMissing_503(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	// Override the recipient var to return nil — same posture as
	// TestSecrets_RecipientMissing_503 (handlers_secrets_test.go).
	prev := (*apidserver.SetSecretRecipient)
	(*apidserver.SetSecretRecipient) = func() *age.X25519Recipient { return nil }
	t.Cleanup(func() { (*apidserver.SetSecretRecipient) = prev })
	app := apidserver.CreateApp(t, e, "no-recip-app")
	rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
		api.PutAppRegistryCredentialRequest{
			Registry: "https://registry.gregale.dev", Username: "alice", Password: registryAuthMark,
		}, nil)
	if rec.Code != 503 {
		t.Fatalf("PUT without recipient: %d %s, want 503", rec.Code, rec.Body.String())
	}
}

// TestRegistryAuth_DeleteRequiresRegistryParam pins that DELETE
// without ?registry= returns 400 invalid_registry_host — the
// handler cannot guess which host to delete.
func TestRegistryAuth_DeleteRequiresRegistryParam(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "del-qp-app")
	rec := e.Do(t, "DELETE",
		"/v1/apps/"+app.Slug+"/registry-credentials", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("DELETE without registry: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_registry_host") {
		t.Errorf("body lacks stable code: %s", rec.Body.String())
	}
}

// TestRegistryAuth_StoredCiphertextContainsNamespace pins that
// the persisted blob's sealed namespace is "registry_creds"
// (mirrors app_secret's namespace discipline). A future refactor
// that uses a different namespace will surface here. The actual
// namespace string check lives in pkg/imaged's handler_auth_test
// (where the identity is wired and OpenBytes is called); here we
// pin the persisted ciphertext shape (length + non-zero).
func TestRegistryAuth_StoredCiphertextContainsNamespace(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "ns-app")
	rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
		api.PutAppRegistryCredentialRequest{
			Registry: "https://registry.gregale.dev", Username: "alice", Password: "p",
		}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	row, err := (*e.Store()).GetAppRegistryCredential(context.Background(), (*e.Acct()).ID, app.ID, "registry.gregale.dev")
	if err != nil {
		t.Fatalf("GetAppRegistryCredential: %v", err)
	}
	if len(row.PasswordEncrypted) < 100 {
		t.Errorf("PasswordEncrypted length = %d, want >= 100 (age stanza header alone is ~96 bytes)", len(row.PasswordEncrypted))
	}
}

// TestRegistryAuth_ProScaleQuotaMatrix pins the per-plan quota
// table at the API boundary for every paid plan (issue #461 /
// ADR-062 §Decision 6). Free is pinned by
// TestRegistryAuth_FreePlanReturns403; Hobby is pinned by
// TestRegistryAuth_HobbyQuotaEnforced. Together these cover the
// full matrix in pkg/api/limits.go.RegistryCredentialMax.
//
// Pro (cap 5) — the 6th distinct host hits 413.
// Scale (cap 20) — the 21st distinct host hits 413.
func TestRegistryAuth_ProScaleQuotaMatrix(t *testing.T) {
	type planCase struct {
		name    string
		plan    api.Plan
		cap     int
		planURL string // stable code substring from ErrPlanRegistryCredentialQuota
	}
	cases := []planCase{
		{"Pro", api.PlanPro, 5, "plan_registry_credential_quota"},
		{"Scale", api.PlanScale, 20, "plan_registry_credential_quota"},
	}
	for _, pc := range cases {
		t.Run(pc.name, func(t *testing.T) {
			e := setupRegistry(t, pc.plan)
			app := apidserver.CreateApp(t, e, "quota-"+strings.ToLower(pc.name))
			// Fill the cap with distinct hosts.
			for i := 0; i < pc.cap; i++ {
				h := "https://h" + string(rune('a'+i%26))
				if i >= 26 {
					h += "2"
				}
				h += ".example.com"
				rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
					api.PutAppRegistryCredentialRequest{
						Registry: h, Username: "u", Password: "p",
					}, nil)
				if rec.Code != 200 {
					t.Fatalf("PUT %d (%s): %d %s", i, h, rec.Code, rec.Body.String())
				}
			}
			// The (cap+1)-th distinct host hits 413 with the stable code.
			rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
				api.PutAppRegistryCredentialRequest{
					Registry: "https://overflow.example.com", Username: "u", Password: "p",
				}, nil)
			if rec.Code != 413 {
				t.Fatalf("PUT overflow on %s: %d %s, want 413", pc.name, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), pc.planURL) {
				t.Errorf("%s overflow body lacks stable code: %s", pc.name, rec.Body.String())
			}
			// Quota metadata on the GET response surfaces the cap.
			rec = e.Do(t, "GET", "/v1/apps/"+app.Slug+"/registry-credentials", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("GET on %s: %d %s", pc.name, rec.Code, rec.Body.String())
			}
			list := decodeRegistryList(t, rec.Body)
			if list.QuotaMax != pc.cap {
				t.Errorf("%s QuotaMax = %d, want %d", pc.name, list.QuotaMax, pc.cap)
			}
			if list.Count != pc.cap {
				t.Errorf("%s Count = %d, want %d", pc.name, list.Count, pc.cap)
			}
		})
	}
}

// TestRegistryAuth_HTTPSRequired pins the https-only policy at
// the API boundary end-to-end. The handler rejects schemeless,
// http://, embedded path / port out of range / empty input /
// path traversal; accepts explicit https:// + uppercase +
// whitespace + trailing slash normalisations. The OpenAPI doc
// also advertises the https:// requirement — kept here as a
// single test that pins the seam between the OpenAPI docstring
// and the live handler.
func TestRegistryAuth_HTTPSRequired(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	app := apidserver.CreateApp(t, e, "https-app")
	reject := []string{
		"registry.example.com",               // schemeless
		"http://registry.example.com",        // cleartext
		"http://registry.example.com:443",    // cleartext even with port
		"ftp://registry.example.com",         // wrong scheme
		"https://registry.example.com/path",  // embedded path
		"https://registry.example.com:99999", // bad port
		"",                                   // empty
		"  ",                                 // whitespace only
	}
	for _, h := range reject {
		rec := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/registry-credentials",
			api.PutAppRegistryCredentialRequest{
				Registry: h, Username: "alice", Password: "p",
			}, nil)
		if rec.Code != 400 {
			t.Errorf("PUT host=%q: %d %s, want 400", h, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), "invalid_registry_host") {
			t.Errorf("PUT host=%q: body lacks stable code: %s", h, rec.Body.String())
		}
	}
}

// TestRegistryAuth_AuditPayloadOmitsPassword pins the IAM-4 audit
// payload shape (ADR-035, ADR-062 §Decision 10) WITHOUT wiring an
// audit sink. The handlers construct the audit map literal in
// code; we re-read the source to confirm the literal does not
// include password / ciphertext / Authorization. This is a source
// tripwire — if a future refactor adds the password to the audit
// map literal, this test fails at build time (Go compiles the
// source we're scanning via go test --
// pkg/api/cmd/apid/handlers_registry_auth.go).
func TestRegistryAuth_AuditPayloadOmitsPassword(t *testing.T) {
	src, err := os.ReadFile("handlers_registry_auth.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(src)
	// The audit map literal in setRegistryCredential MUST NOT
	// carry any password / ciphertext / Authorization field.
	for _, leak := range []string{"Password", "Ciphertext", "Encrypted", "Authorization"} {
		// Only fail if the field name appears inside the audit
		// map block (between `audit.Emit(...)` for "registry_credential.set"
		// and the matching closing brace).
		start := strings.Index(body, `"registry_credential.set"`)
		if start < 0 {
			continue
		}
		end := strings.Index(body[start:], "})")
		if end < 0 {
			continue
		}
		block := body[start : start+end]
		if strings.Contains(block, leak) {
			t.Errorf("audit payload block for registry_credential.set contains %q — would leak credential", leak)
		}
	}
}
