package middleware

import (
	"encoding/base64"
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
	// Flip the last character.
	tampered := tok[:len(tok)-1] + "X"
	if tampered == tok {
		tampered = tok[:len(tok)-2] + "XX"
	}
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
