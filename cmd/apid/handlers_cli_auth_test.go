// Server-side tests for the CLI auth device-code flow (spec §2.2).
//
// The flow:
//
//  1. POST /v1/cli-auth/code       — CLI mints a code.
//  2. GET  /cli-auth?code=…         — Dashboard renders the email form.
//  3. POST /cli-auth                — Dashboard claims the code, sets
//     the session cookie.
//  4. POST /v1/cli-auth/exchange    — CLI polls; on approval, mints
//     the API key and returns plaintext.
//
// The CLI tests in cmd/faas/cli_test.go exercise the wire shape from
// the customer's perspective; these tests exercise the server's
// state machine (claim race, peek vs consume, expired path, unknown
// path, rate-limit wiring). Both pull from the same MemStore via the
// Store interface so the test invariant matches production.

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// newCliAuthTestServer wires a fresh MemStore-backed apid handler
// with the minimum deps needed for the device-code flow: a session
// manager (so /cli-auth POST can issue faas_sid), a noop notifier,
// a noop mailer (login magic-link isn't exercised here but the
// surface is mounted together), a stub githubd client.
func newCliAuthTestServer(t *testing.T) (http.Handler, *state.MemStore) {
	t.Helper()
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "example.com", noopNotifier{}, "",
		noopMailer{}, stubGithubdClient{}, nil, nil, 15*time.Minute, "").handler()
	return srv, store
}

// mintCliAuthCodeForTest is a test helper that POSTs /v1/cli-auth/code
// and returns the parsed response. Uses t.Context() so a slow test
// doesn't outlive its caller.
func mintCliAuthCodeForTest(t *testing.T, srv http.Handler) api.CliAuthCodeResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", nil)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint code: code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	var resp api.CliAuthCodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	return resp
}

// TestMintCliAuthCode_ReturnsCodeAndURL exercises the happy path of
// the anonymous /v1/cli-auth/code endpoint. The code shape must be
// "XXXX-NNNN" (4 hex + dash + 4 hex, case-insensitive); the URL
// must end with the full code as a query parameter so the dashboard
// page picks it up.
func TestMintCliAuthCode_ReturnsCodeAndURL(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)

	resp := mintCliAuthCodeForTest(t, srv)

	parts := strings.Split(resp.Code, "-")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 4 {
		t.Errorf("code = %q, want XXXX-NNNN shape", resp.Code)
	}
	for _, p := range parts {
		for _, c := range p {
			hex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !hex {
				t.Errorf("code %q contains non-hex char %q", resp.Code, c)
			}
		}
	}
	if !strings.HasSuffix(resp.URL, "/cli-auth?code="+resp.Code) {
		t.Errorf("URL = %q, want suffix /cli-auth?code=%s", resp.URL, resp.Code)
	}
	if _, err := time.Parse(time.RFC3339, resp.ExpiresAt); err != nil {
		t.Errorf("ExpiresAt %q is not RFC3339: %v", resp.ExpiresAt, err)
	}
}

// TestCliAuthChain_RateLimited confirms the wiring of
// s.cliAuthLimiter (server.go::cliAuthChain) is the correct bucket.
// The cliAuthChain counts [429, 400] — a real CLI mint succeeds with
// 200 (which we deliberately do NOT count so a customer can retry
// after a transient timeout), but a brute-force on shape-rejected
// bodies still trips the bucket. This test posts a malformed
// exchange body 11 times from the same IP and asserts the 11th
// returns 429.
func TestCliAuthChain_RateLimited(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)
	const ip = "203.0.113.110:55555"

	// Bad JSON body — handler returns 400, which the limiter counts.
	badBody := []byte("{not-json")

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(badBody))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = ip
		srv.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 1; i <= 10; i++ {
		if c := fire(); c != http.StatusBadRequest {
			t.Fatalf("attempt %d: code = %d, want 400 (malformed body)", i, c)
		}
	}
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("11th: code = %d, want 429", c)
	}
}

// TestExchangeCliAuthCode_PendingReturns404 covers the CLI's polling
// path before the user has approved. Mint a code and immediately
// POST /v1/cli-auth/exchange; the server returns 404 with the
// cli_auth_code_pending stable code so the CLI knows to keep waiting.
func TestExchangeCliAuthCode_PendingReturns404(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)

	minted := mintCliAuthCodeForTest(t, srv)
	body, _ := json.Marshal(api.CliAuthExchangeRequest{Code: minted.Code})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404\nbody = %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodeCliAuthPending {
		t.Errorf("problem code = %q, want %q", prob.Code, api.CodeCliAuthPending)
	}
}

// TestClaimThenExchange_ReturnsKey exercises the full happy path
// without going through the dashboard: claim the code directly on
// the store, then exchange. The server must mint a fresh API key,
// return the plaintext in the response, and the plaintext must pass
// ValidAPIKeyFormat (i.e. it is a real fp_live_… token, not garbage).
func TestClaimThenExchange_ReturnsKey(t *testing.T) {
	srv, store := newCliAuthTestServer(t)

	// Seed an account; the claim binds to it.
	acct, err := store.CreateAccount(t.Context(), "carol@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}

	minted := mintCliAuthCodeForTest(t, srv)

	// Pull the hash from the server's mint via a peek (same hash
	// space as the CLI would use). PeekCliAuthCode must report
	// pending, then ClaimCliAuthCode transitions it.
	status, _, err := store.PeekCliAuthCode(t.Context(), mustHashCode(t, minted.Code))
	if err != nil || status != api.CliAuthStatusPending {
		t.Fatalf("peek after mint: status=%v err=%v", status, err)
	}
	if err := store.ClaimCliAuthCode(t.Context(), mustHashCode(t, minted.Code), acct.ID); err != nil {
		t.Fatalf("claim: %v", err)
	}

	body, _ := json.Marshal(api.CliAuthExchangeRequest{Code: minted.Code})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	var resp api.CliAuthExchangeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !api.ValidAPIKeyFormat(resp.Plaintext) {
		t.Errorf("plaintext %q is not a valid api key", resp.Plaintext)
	}
	if resp.Account.Email != "carol@example.com" {
		t.Errorf("account email = %q, want carol@example.com", resp.Account.Email)
	}

	// The exchange endpoint is idempotent in a soft sense: a second
	// call returns a freshly-minted key (the CLI doesn't retry, but a
	// buggy client can't lock itself out). The "single-shot" guarantee
	// is enforced on the dashboard /cli-auth claim side, not here.
	rec2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(body))
	r2.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Errorf("replay code = %d, want 200 (re-exchange yields a fresh key)", rec2.Code)
	}
	var resp2 api.CliAuthExchangeResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if resp2.Plaintext == resp.Plaintext {
		t.Errorf("re-exchange returned the SAME plaintext — keys must differ")
	}
}

// TestClaimCliAuthCode_RaceDoubleClaimReturnsConflict fires two
// concurrent claim attempts against the same pending code with
// different account IDs. The atomic single-row update must let
// exactly one succeed; the other gets state.ErrConflict, which the
// dashboard POST maps to a "code already used" error page.
func TestClaimCliAuthCode_RaceDoubleClaimReturnsConflict(t *testing.T) {
	srv, store := newCliAuthTestServer(t)

	a1, _ := store.CreateAccount(t.Context(), "race1@example.com", api.PlanFree)
	a2, _ := store.CreateAccount(t.Context(), "race2@example.com", api.PlanFree)

	minted := mintCliAuthCodeForTest(t, srv)
	hash := mustHashCode(t, minted.Code)

	type result struct {
		err error
	}
	out := make(chan result, 2)
	go func() { out <- result{store.ClaimCliAuthCode(t.Context(), hash, a1.ID)} }()
	go func() { out <- result{store.ClaimCliAuthCode(t.Context(), hash, a2.ID)} }()

	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		r := <-out
		switch {
		case r.err == nil:
			successes++
		case errors.Is(r.err, state.ErrConflict):
			conflicts++
		default:
			t.Errorf("unexpected claim error: %v", r.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1+1", successes, conflicts)
	}
	_ = srv // silence unused
}

// TestClaimExpiredCode_ReturnsNotFound pastes an expired code into
// the dashboard POST. The claim statement filters on expires_at >
// now() so an expired row is treated as "not found" → ErrConflict
// → dashboard error page.
func TestClaimExpiredCode_ReturnsNotFound(t *testing.T) {
	_, store := newCliAuthTestServer(t)
	acct, _ := store.CreateAccount(t.Context(), "old@example.com", api.PlanFree)

	hash := mustHashCode(t, "DEAD-BEEF")
	pastExpiry := time.Now().Add(-1 * time.Minute)
	if err := store.IssueCliAuthCode(t.Context(), hash, pastExpiry); err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	if err := store.ClaimCliAuthCode(t.Context(), hash, acct.ID); !isConflictOrNotFound(err) {
		t.Errorf("claim expired: err = %v, want ErrConflict or ErrNotFound", err)
	}
}

// TestExchangeCliAuthCode_UnknownCodeReturns410 exercises the
// never-minted path: a code that was never inserted returns
// ErrNotFound from ConsumeCliAuthCode, which the exchange handler
// surfaces as 410 cli_auth_code_unavailable. DEAD-BEEF is valid hex
// (unlike ZZZZ-9999 where Z is not) so we reach the store lookup
// rather than the 400 normalization gate.
func TestExchangeCliAuthCode_UnknownCodeReturns410(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)

	body, _ := json.Marshal(api.CliAuthExchangeRequest{Code: "DEAD-BEEF"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusGone {
		t.Fatalf("code = %d, want 410\nbody = %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != api.CodeCliAuthUnavailable {
		t.Errorf("problem code = %q, want %q", prob.Code, api.CodeCliAuthUnavailable)
	}
}

// TestRenderCliAuthPage_Happy verifies the dashboard GET /cli-auth
// renders the email-input form when a pending code is presented.
// Anti-enumeration: an unknown code renders the error page, NOT a
// 404 (covered in TestRenderCliAuthPage_UnknownRendersError).
func TestRenderCliAuthPage_Happy(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)

	minted := mintCliAuthCodeForTest(t, srv)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cli-auth?code="+minted.Code, nil)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Authorize CLI session") {
		t.Errorf("body missing title: %s", body)
	}
	// The hidden code field carries the normalized (dash-less) code
	// so the POST handler can hash it directly.
	normalized := strings.ReplaceAll(strings.ToUpper(minted.Code), "-", "")
	if !strings.Contains(body, `value="`+normalized+`"`) {
		t.Errorf("body missing hidden code input with value %q: %s", normalized, body)
	}
}

// TestRenderCliAuthPage_UnknownRendersError: a code that was never
// minted must NOT 404 (which would let a phishing page probe which
// codes exist). The dashboard shows an error page instead. The
// shape is valid hex so the code reaches PeekCliAuthCode and the
// "not in store" path; "ZZZZ" was wrong because Z is not hex.
func TestRenderCliAuthPage_UnknownRendersError(t *testing.T) {
	srv, _ := newCliAuthTestServer(t)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cli-auth?code=DEAD-BEEF", nil)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (error page rendered)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Code unavailable") {
		t.Errorf("body missing 'Code unavailable' banner: %s", body)
	}
	if strings.Contains(body, `<form method="POST"`) {
		t.Errorf("body should not contain a form when code is unknown: %s", body)
	}
}

// TestPostCliAuthPage_CreatesAccountOnUnknownEmail exercises the UX
// §2.2 "First successful login creates the account row if the
// email is new" promise. A fresh email + a freshly-minted code must
// result in a 302 to /dashboard/account, a session cookie set, and
// the account now in the store.
func TestPostCliAuthPage_CreatesAccountOnUnknownEmail(t *testing.T) {
	srv, store := newCliAuthTestServer(t)

	minted := mintCliAuthCodeForTest(t, srv)
	form := "code=" + strings.ReplaceAll(minted.Code, "-", "") + "&email=brand-new@example.com"

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/dashboard/account" {
		t.Errorf("redirect = %q, want /dashboard/account", loc)
	}
	if cookie := rec.Header().Get("Set-Cookie"); !strings.Contains(cookie, "faas_sid=") {
		t.Errorf("Set-Cookie = %q, want faas_sid=…", cookie)
	}
	// Verify the account was created.
	acct, err := store.AccountByEmail(t.Context(), "brand-new@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail after create: %v", err)
	}
	if acct.Email != "brand-new@example.com" {
		t.Errorf("account email = %q", acct.Email)
	}
}

// TestPostCliAuthPage_ReusesExistingAccount confirms that a known
// email does not create a duplicate account row — UX §2.2 promise
// is "first successful login creates the row", which means the
// second login reuses the same account_id.
func TestPostCliAuthPage_ReusesExistingAccount(t *testing.T) {
	srv, store := newCliAuthTestServer(t)

	existing, err := store.CreateAccount(t.Context(), "old-customer@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("seed existing: %v", err)
	}

	minted := mintCliAuthCodeForTest(t, srv)
	form := "code=" + strings.ReplaceAll(minted.Code, "-", "") + "&email=old-customer@example.com"

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", rec.Code)
	}
	// Re-lookup; the id must match the pre-existing row.
	got, err := store.AccountByEmail(t.Context(), "old-customer@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail after reuse: %v", err)
	}
	if got.ID != existing.ID {
		t.Errorf("account id changed: was %s, now %s", existing.ID, got.ID)
	}
}

// mustHashCode mirrors what the server does on receipt: strip the
// dash, hex-decode, sha256. Mirrors api.HashToken over the raw 4-byte
// code. Used by tests that need to drive the store directly.
func mustHashCode(t *testing.T, code string) []byte {
	t.Helper()
	normalized := strings.ToUpper(strings.ReplaceAll(code, "-", ""))
	raw, err := hex.DecodeString(normalized)
	if err != nil {
		t.Fatalf("decode hex %q: %v", normalized, err)
	}
	return api.HashToken(raw)
}

// isConflictOrNotFound is the dashboard's "code unavailable" gate —
// either ErrConflict (race lost, already consumed) or ErrNotFound
// (expired, never minted) renders the same error page. Uses
// errors.Is so wrapped errors (e.g. from pgstore.mapErr) match.
func isConflictOrNotFound(err error) bool {
	return errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound)
}

// Compile-time check we still have context available for future
// helpers; keeps imports stable if the file trims a usage.
var _ = context.Background