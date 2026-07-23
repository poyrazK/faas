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
//   - CookieNameAnonymous ("cli-auth:pre") is set on GET /cli-auth
//     and consumed on POST /cli-auth — same browser, no session yet.
//     Subject = the device code from the query string.
//
// In both cases the form field carries the same opaque token value
// ("csrf_token") and Verify cross-checks cookie == form value under
// constant time. The token is a base64url-encoded nonce||ciphertext
// blob; cookie and form are deliberately the same value so the form
// field itself is useless without the cookie.
package middleware

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

// IssueForAnonymous is the /cli-auth equivalent: bound to action +
// deviceCode instead of an account. Same shape, same TTL.
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
// faas_csrf cookie, the csrf_token form field, and the expected
// accountID. Returns nil iff all of:
//
//   - the cookie is present, well-formed, unexpired;
//   - the envelope's Action == action;
//   - the envelope's Subject == accountID;
//   - the form field's value is byte-equal to the cookie value.
//
// Any other shape returns ErrCSRFInvalid (wrapped). Callers should
// respond 400.
func VerifyAuthenticated(manager *session.Manager, r *http.Request, action, accountID string) error {
	if manager == nil {
		return fmt.Errorf("%w: nil session manager", ErrCSRFInvalid)
	}
	c, err := r.Cookie(CookieNameAuthenticated)
	if err != nil || c.Value == "" {
		return fmt.Errorf("%w: missing %s cookie", ErrCSRFInvalid, CookieNameAuthenticated)
	}
	return verifyAgainstForm(manager, r, action, accountID, c.Value)
}

// VerifyAnonymous is the /cli-auth equivalent: expected subject is the
// device code instead of the account_id.
func VerifyAnonymous(manager *session.Manager, r *http.Request, action, deviceCode string) error {
	if manager == nil {
		return fmt.Errorf("%w: nil session manager", ErrCSRFInvalid)
	}
	c, err := r.Cookie(CookieNameAnonymous)
	if err != nil || c.Value == "" {
		return fmt.Errorf("%w: missing %s cookie", ErrCSRFInvalid, CookieNameAnonymous)
	}
	return verifyAgainstForm(manager, r, action, deviceCode, c.Value)
}

func verifyAgainstForm(manager *session.Manager, r *http.Request, action, subject, cookieValue string) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("%w: bad form", ErrCSRFInvalid)
	}
	formValue := r.PostForm.Get(FormFieldName)
	if formValue == "" {
		return fmt.Errorf("%w: missing form field %s", ErrCSRFInvalid, FormFieldName)
	}
	// Cookie value must match the form value (constant time). Both
	// come from the same render call, so they should be byte-equal —
	// but an attacker can flip individual bytes on the form side, and
	// we want any divergence to fail closed.
	if subtle.ConstantTimeCompare([]byte(cookieValue), []byte(formValue)) != 1 {
		return fmt.Errorf("%w: cookie/form mismatch", ErrCSRFInvalid)
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
