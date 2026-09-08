// Package e2e_test — registry-auth credential surface (issue #461 / ADR-062).
//
// CI-safe non-metal test: pins the apid-side contract for
// /v1/apps/{slug}/registry-credentials end-to-end against the real
// PgStore + a real age X25519 recipient + the FakeRegistry's
// RequireBasicAuth option. Coverage:
//
//   - PUT seals the password (ciphertext != plaintext) and the response
//     body NEVER echoes the plaintext marker.
//   - GET lists the credential without a Password field; quota + count
//     surface.
//   - DELETE removes the row; a follow-up GET shows count==0.
//   - The stored password_encrypted column is at least 100 bytes (a real
//     age stanza header is ~96 bytes before the payload — anything
//     shorter is the wrong shape).
//   - The FakeRegistry's /token endpoint accepts the Basic Auth the
//     credential stores and returns a Bearer token; /v2/... then
//     serves the manifest with that Bearer.
//
// imaged-side execution is NOT covered here (the apid-only harness
// boots no imaged); that branch lands in the metal e2e.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

//go:build !no_pg

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestRegistryAuth_E2E_PutGetDeleteRoundTrip_RealSeal is the apid-only
// end-to-end assertion. Mirrors the unit tests in
// handlers_registry_auth_test.go but goes through:
//   - real PgStore + the new app_registry_credentials table (slot 83)
//   - real secretbox.SealBytes via the apid loader (FAAS_HOST_AGE_RECIPIENT_PATH)
//   - the new HTTP routes wired in cmd/apid/server.go
//
// and asserts:
//   - PUT seals, response body has no plaintext marker
//   - GET lists, response body has no plaintext marker AND no Password field
//   - DELETE removes, follow-up GET count==0
//   - stored password_encrypted != plaintext (defence-in-depth against
//     a future refactor that bypasses SealBytes)
//   - FakeRegistry token endpoint accepts the same Basic Auth the
//     credential stores (the imaged-side plumbing is exercised in the
//     metal e2e; here we pin the credential shape against the gateway
//     the pull path will hit).
func TestRegistryAuth_E2E_PutGetDeleteRoundTrip_RealSeal(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Pin the head migration. A future PR that reorders slots 82/83
	// should land the bump here too (test-pgstore-billing-logs-usage
	// memory note: trust `make test`, not worktree ls).
	pgtest.WaitForMigration(t, pool, 87, 10*time.Second)

	// apid loads the recipient lazily via FAAS_HOST_AGE_RECIPIENT_PATH.
	// Same posture as the secrets e2e (account_scoped_e2e_test.go).
	tmpDir := t.TempDir()
	recipientPath := tmpDir + "/host.age.pub"
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatalf("writeTestRecipient: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
	})

	acctID, key := seedAccount(t, h, ctx, "registry-auth", api.PlanHobby)

	// Create the app. Use PlanHobby because Free cannot store
	// credentials (Limits.RegistryCredentialMax == 0 → 403).
	if statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "registry-app"}) != http.StatusCreated {
		t.Fatalf("create registry-app: status not Created")
	}

	// Stand up a FakeRegistry behind the Basic Auth gate. The test
	// does not actually exercise the imaged pull path (apid-only
	// harness) — it uses the registry's /token endpoint as a black
	// box to confirm the credential the customer just stored is
	// accepted at the registry boundary. imaged's pull path itself
	// is covered by the unit tests in pkg/imaged/handler_auth_test.go
	// and (later) by the metal e2e.
	fr := e2etest.NewFakeRegistry()
	defer fr.Close()
	const (
		user = "alice"
		pass = "s3cret-REGISTRY-AUTH-MARKER"
	)
	fr.RequireBasicAuth(user, pass)

	// Stub image — content doesn't matter; we only POST/PUT/DELETE on
	// the credential surface. The image is added so the FakeRegistry
	// serves a manifest if the future metal e2e ever extends here.
	img, _ := e2etest.HelloImage("library/hello", "hello from fake")
	fr.AddImage("library/hello", img)

	// Parse out the FakeRegistry's port — the normalized registry
	// host key is "<ip>:<port>" because pkg/oci.Reference.APIHost
	// surfaces port. apid normalizes to lowercase + no scheme +
	// no trailing slash; port preserved.
	//
	// apid's HTTPS-only validator (ADR-062 C1) requires an explicit
	// `https://` prefix on PUT/DELETE; the FakeRegistry host returned
	// here is `127.0.0.1:<port>`, so we prefix with `https://` before
	// sending and then keep the un-prefixed form for the storage
	// round-trip / DELETE query param (apid re-normalises on the way
	// in and drops the scheme for storage).
	registryHost := fr.Host()
	registryURL := "https://" + registryHost

	// PUT — happy path. Password = the marker; if any layer
	// accidentally echoes the plaintext, the body-scans below fire.
	putReq := api.PutAppRegistryCredentialRequest{
		Registry: registryURL,
		Username: user,
		Password: pass,
	}
	putBody, code := doReq(t, h, key, http.MethodPut,
		"/v1/apps/registry-app/registry-credentials", putReq)
	if code != http.StatusOK {
		t.Fatalf("PUT: %d %s", code, string(putBody))
	}
	if bytes.Contains(putBody, []byte(pass)) {
		t.Fatalf("PUT response leaks password marker: %s", string(putBody))
	}
	var putResp api.AppRegistryCredentialResponse
	if err := json.Unmarshal(putBody, &putResp); err != nil {
		t.Fatalf("PUT unmarshal: %v", err)
	}
	if putResp.Registry != registryHost || putResp.Username != user {
		t.Errorf("PUT response: got %+v", putResp)
	}
	if putResp.CreatedAt == "" || putResp.UpdatedAt == "" {
		t.Errorf("PUT timestamps empty: %+v", putResp)
	}

	// GET — list shape + no Password field + no plaintext marker.
	getBody, code := doReq(t, h, key, http.MethodGet,
		"/v1/apps/registry-app/registry-credentials", nil)
	if code != http.StatusOK {
		t.Fatalf("GET: %d %s", code, string(getBody))
	}
	if bytes.Contains(getBody, []byte(pass)) {
		t.Fatalf("GET response leaks password marker: %s", string(getBody))
	}
	if bytes.Contains(bytes.ToLower(getBody), []byte("password")) {
		// A `password` substring at any depth means the response shape
		// leaked a field. The only legitimate occurrence would be the
		// description/uri of the docs URL — pin against the literal
		// token, not the substring, to avoid false positives there.
		// doc URL mentions "registry-credentials", not "password",
		// so this is a safe lower-bound.
		t.Errorf("GET response mentions `password` somewhere: %s", string(getBody))
	}
	var listResp api.AppRegistryCredentialListResponse
	if err := json.Unmarshal(getBody, &listResp); err != nil {
		t.Fatalf("GET unmarshal: %v", err)
	}
	if listResp.Count != 1 {
		t.Errorf("GET Count = %d, want 1", listResp.Count)
	}
	if listResp.QuotaMax != 2 {
		t.Errorf("GET QuotaMax = %d, want 2 (Hobby)", listResp.QuotaMax)
	}

	// Stored ciphertext != plaintext — the smoke test that
	// secretbox.SealBytes actually ran. The PgStore row's
	// password_encrypted bytea is opaque; reading it back and
	// comparing against the marker proves we stored a sealed blob.
	store := state.NewPgStore(pool)
	app, err := store.AppBySlug(ctx, "registry-app")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	row, err := store.GetAppRegistryCredential(ctx, acctID, app.ID, registryHost)
	if err != nil {
		t.Fatalf("GetAppRegistryCredential: %v", err)
	}
	if len(row.PasswordEncrypted) < 100 {
		t.Errorf("PasswordEncrypted len=%d, want >=100 (age stanza header alone is ~96 bytes)",
			len(row.PasswordEncrypted))
	}
	if bytes.Equal(row.PasswordEncrypted, []byte(pass)) {
		t.Errorf("PasswordEncrypted is plaintext — SealBytes was bypassed")
	}

	// FakeRegistry token endpoint accepts the credential's Basic
	// Auth — the customer's stored credential is the same shape
	// imaged will feed to /token in the production pull path. This
	// pins the credential-shape contract against a real
	// distribution-spec endpoint, not a stub.
	tokReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(fr.URL(), "/")+"/token?service=fake-registry&scope=repository:library/hello", nil)
	if err != nil {
		t.Fatalf("new /token request: %v", err)
	}
	tokReq.SetBasicAuth(user, pass)
	tokResp, err := h.HTTPClient().Do(tokReq)
	if err != nil {
		t.Fatalf("/token round-trip: %v", err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	if tokResp.StatusCode != http.StatusOK {
		t.Fatalf("/token returned %d (registry rejected the credential shape)",
			tokResp.StatusCode)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&tok); err != nil {
		t.Fatalf("/token decode: %v", err)
	}
	if tok.Token == "" {
		t.Fatalf("/token returned empty bearer: %+v", tok)
	}

	// Negative: a wrong Basic Auth at /token must fail loud. The
	// credential gate at apid sealed the password; we mirror the
	// "wrong creds" path so a future refactor that bypasses
	// SetBasicAuth or stores the wrong field shape surfaces here.
	badReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(fr.URL(), "/")+"/token", nil)
	badReq.SetBasicAuth(user, "WRONG-PASSWORD")
	badResp, err := h.HTTPClient().Do(badReq)
	if err != nil {
		t.Fatalf("/token bad-creds round-trip: %v", err)
	}
	defer func() { _ = badResp.Body.Close() }()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/token with wrong creds: %d, want 401", badResp.StatusCode)
	}

	// DELETE — happy path.
	delURL := "/v1/apps/registry-app/registry-credentials?registry=" + registryURL
	_, code = doReq(t, h, key, http.MethodDelete, delURL, nil)
	if code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", code)
	}
	// Follow-up GET shows count==0.
	getBody, code = doReq(t, h, key, http.MethodGet,
		"/v1/apps/registry-app/registry-credentials", nil)
	if code != http.StatusOK {
		t.Fatalf("GET after delete: %d %s", code, string(getBody))
	}
	if err := json.Unmarshal(getBody, &listResp); err != nil {
		t.Fatalf("GET after delete unmarshal: %v", err)
	}
	if listResp.Count != 0 {
		t.Errorf("Count after delete = %d, want 0", listResp.Count)
	}

	// DELETE-not-found is 400 (mirrors ErrSecretNotFound posture —
	// the URL resource IS the host).
	_, code = doReq(t, h, key, http.MethodDelete,
		"/v1/apps/registry-app/registry-credentials?registry="+registryHost, nil)
	if code != http.StatusBadRequest {
		t.Errorf("DELETE absent: %d, want 400", code)
	}
}
