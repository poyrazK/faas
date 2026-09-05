// Package middleware — CSRF token helpers (security review A1 + A3).
//
// The token model is a sealed blob the helper binds to:
//
//   - a per-action action name (e.g. "delete", "restore", "cli-auth"),
//     so a stolen "delete" token can't be replayed against "restore";
//   - a per-render subject — either the authenticated account ID
//     (for /dashboard/* forms) or the device code (for the anonymous
//     /cli-auth claim form);
//   - a TTL (10 minutes by default).
//
// The blob is sealed with the same AES-256-GCM session.Manager that
// seals the faas_sid cookie, so an attacker without the 32-byte host
// secret cannot forge a token. The session.Manager zeroes its key on
// NewManager so the secret never leaves the AEAD's internal copy.
//
// Two cookie shapes:
//
//   - CookieNameAuthenticated ("faas_csrf") is set on the rendering
//     page (e.g. GET /dashboard/account) and consumed on the POST.
//     Subject = account_id from the sessionAuth context.
//
//   - CookieNameAnonymous ("cli-auth:pre") is available for anonymous
//     form surfaces that bind to a device code. The CLI authorization
//     page itself uses CookieNameAuthenticated because it requires the
//     normal dashboard session before a code can be claimed.
//
// In both cases the form field carries the same opaque token value
// ("csrf_token") and Verify cross-checks cookie == form value under
// constant time. The token is a base64url-encoded nonce||ciphertext
// blob; cookie and form are deliberately the same value so the form
// field itself is useless without the cookie.
//
// Transport shape: legacy form-encoded requests carry the token in
// the `csrf_token` form field; JSON-body requests (the apid API used
// by the dashboard's JS client and the IAM-2 / issue #186 MFA
// handlers /verify + /confirm + /recover + /disable) carry the same
// field as a sibling JSON property. VerifyAuthenticated tries the
// form first, then the JSON body — both branches resolve to the
// same opaque token value, so the cookie-constant-time compare and
// the action/subject/expired envelope checks are identical across
// the two transports. The handler that decodes the request body
// (the MFA handlers, `decodeJSON` below) still gets a usable
// io.Reader because we restore the body after peeking.
package middleware

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
)

// Cookie names. Both are HttpOnly + Secure + SameSite=Lax at the call
// site (the helper does not own cookie attributes — see the call sites
// in cmd/apid/handlers_dashboard.go and cmd/apid/handlers_cli_auth.go).
const (
	// CookieNameAuthenticated carries a CSRF token bound to an
	// authenticated account. Set on every dashboard GET that renders a
	// form-bearing page; consumed on the matching POST.
	CookieNameAuthenticated = "faas_csrf"
	// CookieNameAnonymous carries a CSRF token bound to a device code.
	// Set on GET /cli-auth; consumed on POST /cli-auth. Note: Go's
	// http.Cookie parser strips ':' from cookie names (RFC 6265 token
	// grammar), so we use '-' as the separator here.
	CookieNameAnonymous = "cli-auth-pre"
	// FormFieldName is the form field every protected form carries.
	// Renamed from "confirm_token" to make the new contract grep-able
	// and explicit.
	FormFieldName = "csrf_token"
)

// DefaultCSRFTTL is longer than the device-code TTL (5 min) so a
// customer who takes their time entering the email still has a valid
// token; short enough that a leaked token has a tight blast radius.
const DefaultCSRFTTL = 10 * time.Minute

// ErrCSRFInvalid is returned by Verify on any mismatch. Callers should
// map this to a 400 response. Wrapping preserves the kind for tests.
var ErrCSRFInvalid = errors.New("csrf: token invalid or expired")

// envelope is the JSON payload sealed inside the CSRF blob. Adding a
// field here is non-breaking for newer clients opening older blobs
// only if old clients tolerate unknown fields — json.Unmarshal does
// by default, so we're safe.
//
// Subject is either an account_id (authenticated surface) or a device
// code (anonymous surface); callers must ensure it is the value they
// expect on Verify.
type envelope struct {
	Action  string    `json:"action"`
	Subject string    `json:"subject"`
	Expires time.Time `json:"expires"`
}

// IssueForAuthenticated binds a CSRF token to action + accountID. The
// returned token is the cookie value AND the form field value — they
// are deliberately identical so the form field alone is useless to an
// attacker (no same-origin cookie → can't even submit). The caller
// sets the cookie with the canonical attribute set and renders the
// token into the form. manager is the same session.Manager that seals
// faas_sid so we don't need a new key.
//
// Errors only on session.Manager init failure (which the caller has
// already handled — Manager is wired at boot).
func IssueForAuthenticated(manager *session.Manager, action, accountID string) (string, error) {
	return issue(manager, action, accountID, DefaultCSRFTTL)
}

// IssueForAuthenticatedNamed is the multi-form-page variant of
// IssueForAuthenticated. The caller supplies the cookie name that its matching
// verifier will read, allowing independently action-bound tokens to coexist on
// one rendered page. Existing single-form callers keep using
// CookieNameAuthenticated through IssueForAuthenticated.
func IssueForAuthenticatedNamed(manager *session.Manager, action, accountID, cookieName string) (string, error) {
	if err := validateCSRFCookieName(cookieName); err != nil {
		return "", err
	}
	return issue(manager, action, accountID, DefaultCSRFTTL)
}

// IssueForAnonymous is the anonymous-form equivalent: bound to action +
// deviceCode instead of an account. The CLI authorization page uses
// IssueForAuthenticated so its code claim is tied to the signed-in account.
func IssueForAnonymous(manager *session.Manager, action, deviceCode string) (string, error) {
	return issue(manager, action, deviceCode, DefaultCSRFTTL)
}

func issue(manager *session.Manager, action, subject string, ttl time.Duration) (string, error) {
	if manager == nil {
		return "", errors.New("csrf: nil session manager")
	}
	if action == "" || subject == "" {
		return "", errors.New("csrf: empty action or subject")
	}
	env := envelope{
		Action:  action,
		Subject: subject,
		Expires: time.Now().Add(ttl),
	}
	plaintext, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("csrf: marshal envelope: %w", err)
	}
	sealed, err := manager.SealForCSRF(plaintext)
	if err != nil {
		return "", fmt.Errorf("csrf: seal: %w", err)
	}
	return sealed, nil
}

// VerifyAuthenticated is the dashboard-side verifier. It reads the
// faas_csrf cookie, the csrf_token form field OR the
// `csrf_token` JSON-body field, and the expected accountID.
// Returns nil iff all of:
//
//   - the cookie is present, well-formed, unexpired;
//   - the envelope's Action == action;
//   - the envelope's Subject == accountID;
//   - the token field value (form or JSON) is byte-equal to the
//     cookie value.
//
// Any other shape returns ErrCSRFInvalid (wrapped). Callers should
// respond 400.
//
// Transport: form-encoded requests are read via r.ParseForm() +
// r.PostForm.Get(). JSON requests are peeked byte-by-byte (and
// the body is restored with io.NopCloser(bytes.NewReader(...)) so
// the handler's downstream JSON decoder sees the original payload
// intact).
func VerifyAuthenticated(manager *session.Manager, r *http.Request, action, accountID string) error {
	return VerifyAuthenticatedNamed(manager, r, action, accountID, CookieNameAuthenticated)
}

// VerifyAuthenticatedNamed verifies an authenticated token against the named
// sidecar cookie. This preserves per-action envelopes while allowing several
// protected forms to be rendered on the same page without overwriting the
// single faas_csrf cookie.
func VerifyAuthenticatedNamed(manager *session.Manager, r *http.Request, action, accountID, cookieName string) error {
	if manager == nil {
		return fmt.Errorf("%w: nil session manager", ErrCSRFInvalid)
	}
	if err := validateCSRFCookieName(cookieName); err != nil {
		return fmt.Errorf("%w: %v", ErrCSRFInvalid, err)
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return fmt.Errorf("%w: missing %s cookie", ErrCSRFInvalid, cookieName)
	}
	return verifyAgainstRequest(manager, r, action, accountID, c.Value)
}

func validateCSRFCookieName(cookieName string) error {
	if cookieName == "" {
		return errors.New("csrf: empty cookie name")
	}
	if err := (&http.Cookie{Name: cookieName, Value: "csrf"}).Valid(); err != nil {
		return fmt.Errorf("csrf: invalid cookie name: %w", err)
	}
	return nil
}

// VerifyAnonymous verifies an anonymous form token whose subject is a
// device code. Authenticated dashboard forms use VerifyAuthenticated.
func VerifyAnonymous(manager *session.Manager, r *http.Request, action, deviceCode string) error {
	if manager == nil {
		return fmt.Errorf("%w: nil session manager", ErrCSRFInvalid)
	}
	c, err := r.Cookie(CookieNameAnonymous)
	if err != nil || c.Value == "" {
		return fmt.Errorf("%w: missing %s cookie", ErrCSRFInvalid, CookieNameAnonymous)
	}
	return verifyAgainstRequest(manager, r, action, deviceCode, c.Value)
}

// verifyAgainstRequest is the shared body of VerifyAuthenticated +
// VerifyAnonymous. The "form vs JSON" half of the transport splits
// into extractRequestToken; the constant-time compare and envelope
// checks are identical across both transports because the cookie +
// the token value carry the same opaque seal.
func verifyAgainstRequest(manager *session.Manager, r *http.Request, action, subject, cookieValue string) error {
	tokenValue, err := extractRequestToken(r)
	if err != nil {
		return err
	}
	// Cookie value must match the token value (constant time). Both
	// come from the same render call, so they should be byte-equal —
	// but an attacker can flip individual bytes on the wire, and we
	// want any divergence to fail closed.
	if subtle.ConstantTimeCompare([]byte(cookieValue), []byte(tokenValue)) != 1 {
		return fmt.Errorf("%w: cookie/request mismatch", ErrCSRFInvalid)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookieValue)
	if err != nil {
		return fmt.Errorf("%w: bad base64", ErrCSRFInvalid)
	}
	plaintext, err := manager.OpenForCSRF(raw)
	if err != nil {
		// Wrap both: ErrCSRFInvalid so callers errors.Is for the
		// sentinel, plus the inner session error for diagnostic logs.
		return fmt.Errorf("%w: open: %w", ErrCSRFInvalid, err)
	}
	var env envelope
	if err := json.Unmarshal(plaintext, &env); err != nil {
		return fmt.Errorf("%w: bad envelope", ErrCSRFInvalid)
	}
	if env.Expires.Before(time.Now()) {
		return fmt.Errorf("%w: expired", ErrCSRFInvalid)
	}
	if env.Action != action {
		return fmt.Errorf("%w: action mismatch", ErrCSRFInvalid)
	}
	if env.Subject != subject {
		return fmt.Errorf("%w: subject mismatch", ErrCSRFInvalid)
	}
	return nil
}

// extractRequestToken pulls the csrf_token value from the request
// body — either the form field (legacy dashboard form encoding) or
// the JSON-body `csrf_token` sibling (the JSON API used by the
// dashboard's JS client + the IAM-2 / issue #186 MFA handlers).
// Returns ErrCSRFInvalid-wrapped when neither path yields a token.
//
// Side effect on the JSON path: r.Body is replaced with a fresh
// io.NopCloser wrapping the peeked bytes so the handler's
// downstream JSON decoder still sees the original payload. The
// peeked-bytes shape is mandatory: without it the MFA handlers'
// decodeJSON calls would observe an empty body.
func extractRequestToken(r *http.Request) (string, error) {
	// Form path — only when the request advertises a form
	// Content-Type. r.ParseForm is cheap and idempotent; failure
	// here means a malformed form, which we treat as "no token
	// found" so the JSON path can still try.
	if isFormContentType(r.Header.Get("Content-Type")) {
		if err := r.ParseForm(); err == nil {
			if v := r.PostForm.Get(FormFieldName); v != "" {
				return v, nil
			}
		}
	}
	// JSON path — only when Content-Type starts with
	// application/json. We peek the body, decode a small
	// `{csrf_token: ...}` shape (ignoring every other sibling field
	// via a "known JSON object" decode), and restore the body via
	// io.NopCloser(bytes.NewReader(...)) so the handler re-reads
	// the original bytes intact.
	if isJSONContentType(r.Header.Get("Content-Type")) {
		if r.Body == nil {
			return "", fmt.Errorf("%w: empty JSON body", ErrCSRFInvalid)
		}
		raw, readErr := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("%w: read JSON body: %w", ErrCSRFInvalid, readErr)
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		// Decoding into a permissive shape keeps the door open for
		// future MFA-related fields without a constant-time scan.
		var peek struct {
			CsrfToken string `json:"csrf_token"`
		}
		if err := json.Unmarshal(raw, &peek); err == nil && peek.CsrfToken != "" {
			return peek.CsrfToken, nil
		}
		// JSON decode failed or the field is absent — fall through
		// to the ErrCSRFInvalid sentinel so the caller writes a
		// 400, not a panic.
	}
	return "", fmt.Errorf("%w: missing %s field", ErrCSRFInvalid, FormFieldName)
}

func isFormContentType(ct string) bool {
	// application/x-www-form-urlencoded OR multipart/form-data.
	// We deliberately do NOT sniff charset: a missing charset is
	// the common case for form posts and matches the dashboard
	// <form> submissions.
	return ct == "application/x-www-form-urlencoded" ||
		(len(ct) >= 19 && ct[:19] == "multipart/form-data")
}

func isJSONContentType(ct string) bool {
	// application/json with optional charset; matches what the JS
	// dashboard client sets for /v1/account/mfa/*.
	return ct == "application/json" ||
		(len(ct) >= 16 && ct[:16] == "application/json;")
}
