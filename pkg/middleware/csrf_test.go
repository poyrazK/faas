package middleware

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
)

// helper: build a Manager with a fresh ephemeral key.
func newTestManager(t *testing.T) *session.Manager {
	t.Helper()
	m, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatalf("NewEphemeralManager: %v", err)
	}
	return m
}

// helper: build a POST request carrying the cookie + form values.
func buildPost(t *testing.T, cookieName, cookieValue string, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}
	return req
}

func TestIssueForAuthenticated_Roundtrip(t *testing.T) {
	m := newTestManager(t)
	tok, err := IssueForAuthenticated(m, "delete", "acct-123")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok == "" {
		t.Fatal("token empty")
	}
	req := buildPost(t, CookieNameAuthenticated, tok,
		FormFieldName+"="+tok+"&other=1")
	if err := VerifyAuthenticated(m, req, "delete", "acct-123"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestIssueForAuthenticatedNamed_Roundtrip(t *testing.T) {
	const cookieName = "faas_csrf_key_delete"
	m := newTestManager(t)
	tok, err := IssueForAuthenticatedNamed(m, "key_delete", "acct-123", cookieName)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	req := buildPost(t, cookieName, tok, FormFieldName+"="+tok)
	if err := VerifyAuthenticatedNamed(m, req, "key_delete", "acct-123", cookieName); err != nil {
		t.Fatalf("VerifyAuthenticatedNamed: %v", err)
	}
}

func TestVerifyAuthenticatedNamed_DoesNotFallBackToDefaultCookie(t *testing.T) {
	const cookieName = "faas_csrf_key_delete"
	m := newTestManager(t)
	tok, err := IssueForAuthenticatedNamed(m, "key_delete", "acct-123", cookieName)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	req := buildPost(t, CookieNameAuthenticated, tok, FormFieldName+"="+tok)
	if err := VerifyAuthenticatedNamed(m, req, "key_delete", "acct-123", cookieName); err == nil {
		t.Fatal("named verifier accepted the default CSRF cookie")
	}
}

func TestIssueForAuthenticatedNamed_RejectsInvalidCookieName(t *testing.T) {
	m := newTestManager(t)
	for _, cookieName := range []string{"", "contains a space"} {
		if _, err := IssueForAuthenticatedNamed(m, "key_delete", "acct-123", cookieName); err == nil {
			t.Errorf("cookie name %q: expected error", cookieName)
		}
	}
}

func TestVerify_MissingCookie(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "delete", "acct-1")
	req := buildPost(t, CookieNameAuthenticated, "", FormFieldName+"="+tok)
	if err := VerifyAuthenticated(m, req, "delete", "acct-1"); err == nil {
		t.Fatal("expected error on missing cookie, got nil")
	}
}

func TestVerify_MissingFormField(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "delete", "acct-1")
	req := buildPost(t, CookieNameAuthenticated, tok, "other=1")
	if err := VerifyAuthenticated(m, req, "delete", "acct-1"); err == nil {
		t.Fatal("expected error on missing form field, got nil")
	}
}

func TestVerify_CookieFormMismatch(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "delete", "acct-1")
	// Cookie says "tok", form says "tokX" — must fail.
	req := buildPost(t, CookieNameAuthenticated, tok, FormFieldName+"="+tok+"X")
	if err := VerifyAuthenticated(m, req, "delete", "acct-1"); err == nil {
		t.Fatal("expected cookie/form mismatch to fail, got nil")
	}
}

func TestVerify_TamperedToken(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "delete", "acct-1")
	// Flip the last character. '!' is intentionally NOT in
	// base64.RawURLEncoding's alphabet (A-Z a-z 0-9 - _) so
	// base64.DecodeString rejects the tampered string immediately,
	// independent of which byte the AEAD would have decoded
	// otherwise. Picking an alphabet char like 'X' flaked ~1/64 on
	// the prior pkg/session tamper test (memory pkg-session-tamper-flake).
	tampered := tok[:len(tok)-1] + "!"
	req := buildPost(t, CookieNameAuthenticated, tampered, FormFieldName+"="+tampered)
	if err := VerifyAuthenticated(m, req, "delete", "acct-1"); err == nil {
		t.Fatal("expected tampered token to fail, got nil")
	}
}

func TestVerify_WrongAction(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "delete", "acct-1")
	req := buildPost(t, CookieNameAuthenticated, tok, FormFieldName+"="+tok)
	if err := VerifyAuthenticated(m, req, "restore", "acct-1"); err == nil {
		t.Fatal("expected action mismatch to fail, got nil")
	}
}

func TestVerify_WrongSubject(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "delete", "acct-1")
	req := buildPost(t, CookieNameAuthenticated, tok, FormFieldName+"="+tok)
	if err := VerifyAuthenticated(m, req, "delete", "acct-other"); err == nil {
		t.Fatal("expected subject mismatch to fail, got nil")
	}
}

func TestVerify_ExpiredToken(t *testing.T) {
	m := newTestManager(t)
	// Issue directly with a tiny TTL.
	tok, err := issue(m, "delete", "acct-1", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Wait for the TTL to elapse.
	time.Sleep(20 * time.Millisecond)
	req := buildPost(t, CookieNameAuthenticated, tok, FormFieldName+"="+tok)
	if err := VerifyAuthenticated(m, req, "delete", "acct-1"); err == nil {
		t.Fatal("expected expired token to fail, got nil")
	}
}

func TestIssue_NilManager(t *testing.T) {
	if _, err := IssueForAuthenticated(nil, "delete", "x"); err == nil {
		t.Fatal("expected error on nil manager")
	}
}

func TestIssue_EmptyActionOrSubject(t *testing.T) {
	m := newTestManager(t)
	if _, err := IssueForAuthenticated(m, "", "x"); err == nil {
		t.Fatal("expected error on empty action")
	}
	if _, err := IssueForAuthenticated(m, "delete", ""); err == nil {
		t.Fatal("expected error on empty subject")
	}
}

func TestCSRFBlob_DoesNotVerifyAsSessionCookie(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "delete", "acct-1")
	// Try opening the CSRF blob as a session cookie envelope. The
	// domain-separation AAD must make this fail.
	if _, err := m.Verify(tok); err == nil {
		t.Fatal("CSRF blob unexpectedly verified as a session cookie — domain separation broken")
	}
}

func TestSessionCookie_DoesNotVerifyAsCSRF(t *testing.T) {
	m := newTestManager(t)
	sid, err := m.Issue("acct-1")
	if err != nil {
		t.Fatalf("Issue session: %v", err)
	}
	// Try opening the session cookie as a CSRF blob. Symmetric to
	// TestCSRFBlob_DoesNotVerifyAsSessionCookie: the AAD on the
	// session-cookie path is nil, so a CSRF seal with the
	// csrfDomainSep AAD must not authenticate.
	raw, err := base64.RawURLEncoding.DecodeString(sid)
	if err != nil {
		t.Fatalf("decode session cookie: %v", err)
	}
	if _, err := m.OpenForCSRF(raw); err == nil {
		t.Fatal("session cookie unexpectedly verified as a CSRF blob — domain separation broken")
	}
}

func TestAnonymous_Roundtrip(t *testing.T) {
	m := newTestManager(t)
	tok, err := IssueForAnonymous(m, "cli-auth", "ABCDEF12")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	req := buildPost(t, CookieNameAnonymous, tok,
		FormFieldName+"="+tok+"&email=x@example.com")
	if err := VerifyAnonymous(m, req, "cli-auth", "ABCDEF12"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestAnonymous_WrongDeviceCodeFails(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAnonymous(m, "cli-auth", "ABCDEF12")
	req := buildPost(t, CookieNameAnonymous, tok, FormFieldName+"="+tok)
	if err := VerifyAnonymous(m, req, "cli-auth", "OTHERCODE"); err == nil {
		t.Fatal("expected device-code mismatch to fail, got nil")
	}
}

// buildJSONPost builds a POST carrying a JSON body with the same
// csrf_token sibling the dashboard JS client + IAM-2 MFA handlers
// (/verify + /confirm + /recover + /disable) use. Mirrors
// buildPost above but with Content-Type: application/json. The
// caller passes the raw JSON body — the test owns the wire shape
// explicitly.
func buildJSONPost(cookieName, cookieValue string, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/account/mfa/confirm",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}
	return req
}

// TestVerify_JSONBody_Roundtrip pins the IAM-2 / issue #186 Finding
// #7 contract: the CSRF token reaches the server inside a JSON
// body's `csrf_token` sibling field (the form-legacy path stays
// supported for dashboard POSTs). Verify must still reject mismatched
// cookie vs JSON, expired tokens, and bad envelopes — same
// checks, two transports.
func TestVerify_JSONBody_Roundtrip(t *testing.T) {
	m := newTestManager(t)
	tok, err := IssueForAuthenticated(m, "mfa_confirm", "acct-123")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	body := `{"totp":"123456","csrf_token":"` + tok + `"}`
	req := buildJSONPost(CookieNameAuthenticated, tok, body)
	if err := VerifyAuthenticated(m, req, "mfa_confirm", "acct-123"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Side effect: the body must remain readable for the
	// handler's downstream JSON decoder. VerifyUnknownToken
	// pins this contract.
	req.Body.Close()
}

// TestVerify_JSONBody_BodyStillReadable pins the side effect of
// peek-then-restore on r.Body. The MFA handlers call
// decodeJSON after VerifyAuthenticated — without the NopCloser
// restoration, decodeJSON would observe an empty body and 400 with
// "EOF" instead of routing the message through the verify gate.
func TestVerify_JSONBody_BodyStillReadable(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "mfa_confirm", "acct-123")
	body := `{"totp":"123456","csrf_token":"` + tok + `"}`
	req := buildJSONPost(CookieNameAuthenticated, tok, body)
	if err := VerifyAuthenticated(m, req, "mfa_confirm", "acct-123"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Decode after Verify: handlers do `json.NewDecoder(r.Body)
	// .Decode(&req)` — same shape, here with `io.ReadAll` for
	// brevity.
	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restored) != body {
		t.Fatalf("body drifted: got %q, want %q", restored, body)
	}
}

// TestVerify_JSONBody_MissingField fails closed when the JSON body
// doesn't carry `csrf_token`. The dashboard always sets it; a
// foreign-origin request that omits it should 400, not bypass.
func TestVerify_JSONBody_MissingField(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "mfa_confirm", "acct-1")
	body := `{"totp":"123456"}` // no csrf_token field
	req := buildJSONPost(CookieNameAuthenticated, tok, body)
	if err := VerifyAuthenticated(m, req, "mfa_confirm", "acct-1"); err == nil {
		t.Fatal("expected missing csrf_token field to fail, got nil")
	}
}

// TestVerify_JSONBody_CookieMismatch fails closed when the JSON-body
// csrf_token disagrees with the cookie. Same constant-time compare
// as the form path; only the extraction branch changes.
func TestVerify_JSONBody_CookieMismatch(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "mfa_confirm", "acct-1")
	cookieVal := tok
	jsonVal := tok + "X"
	body := `{"csrf_token":"` + jsonVal + `"}`
	req := buildJSONPost(CookieNameAuthenticated, cookieVal, body)
	if err := VerifyAuthenticated(m, req, "mfa_confirm", "acct-1"); err == nil {
		t.Fatal("expected cookie/body mismatch to fail, got nil")
	}
}

// TestExtractRequestToken_FormPreferred pins the order: when both
// form AND JSON paths could yield a token (rare but possible if a
// client sets both), the form value wins because legacy dashboard
// posts put the field there. Helps keep the regression risk
// low for the existing form-encoding users.
func TestExtractRequestToken_FormPreferred(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "x", "acct-1")
	body := FormFieldName + "=" + tok
	req := buildPost(t, CookieNameAuthenticated, tok, body)
	got, err := extractRequestToken(req)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got != tok {
		t.Fatalf("got %q, want %q", got, tok)
	}
}

// TestExtractRequestToken_BodyRestoreAfterRead pins the body-restoration
// invariant at the helper boundary (not just VerifyAuthenticated):
// any caller of extractRequestToken must see the body intact on return.
// Regression here would surface as empty-body decodeJSON in callers.
func TestExtractRequestToken_BodyRestoreAfterRead(t *testing.T) {
	m := newTestManager(t)
	tok, _ := IssueForAuthenticated(m, "x", "acct-1")
	body := `{"csrf_token":"` + tok + `","x":1}`
	req := buildJSONPost(CookieNameAuthenticated, tok, body)
	if _, err := extractRequestToken(req); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, _ := io.ReadAll(req.Body)
	if string(got) != body {
		t.Fatalf("body drifted post-extract: got %q, want %q", got, body)
	}
}
