// MFA wire shapes (IAM-2, issue #186).
//
// The MFA flow is seven POST endpoints under /v1/account/mfa/*:
//
//   - /enroll  — start enrollment. Returns the otpauth URL +
//                secret + QR + recovery codes ONCE. The dashboard
//                renders the QR + recovery-code list; the
//                plaintext secret is also returned so the
//                customer's authenticator app can ingest the URL
//                by copy-paste.
//
//   - /confirm — finish enrollment. Body {totp}. Verifies the
//                code against the sealed secret, stamps
//                mfa_enrolled_at, clears mfa_required, and
//                re-issues the session cookie without
//                mfa_pending. Idempotent on retry.
//
//   - /verify  — step up an mfa_pending session. Body {totp}.
//                Same verify path as /confirm but does NOT
//                stamp mfa_enrolled_at (the customer is
//                already enrolled). Re-issues the cookie
//                without mfa_pending.
//
//   - /recover — burn a recovery code to regain access when
//                the TOTP device is lost. Body {code}.
//                Removes the matching hash from the stored
//                set; re-issues the cookie without mfa_pending.
//
//   - /disable — opt out. Body {password} OR {recovery_code}.
//                Re-authenticates the customer by proving the
//                password (the account_passwords hash still
//                applies) or by burning a recovery code.
//                Clears mfa_secret_encrypted +
//                mfa_recovery_codes_hash + mfa_enrolled_at;
//                leaves mfa_required untouched so the
//                chokepoints can re-arm on the next trigger.
//
//   - /disable-email — authenticated fallback when the password and
//                recovery codes are unavailable. Sends a one-time link
//                and starts a server-side 24-hour cooldown.
//
//   - /disable-email/confirm — body {token}. After the cooldown,
//                consumes the one-time token and clears MFA.
//
// All seven responses are 200 OK on success. Failure modes use
// the RFC 7807 problem shape: CodeMFAInvalidCode (401) for bad
// codes, CodeNotFound (404) when the account row is gone
// between verify and the database stamp, CodeConflict (409)
// when /enroll is called by an already-enrolled customer.

package api

// CSRFTokenResponse is an action-bound token for browser clients. The
// corresponding cookie remains HttpOnly; returning the token in JSON lets a
// single-page app submit CSRF-protected mutations without exposing the cookie
// to JavaScript.
type CSRFTokenResponse struct {
	CSRFToken string `json:"csrf_token"`
}

// MFAEnrollRequest is empty — the customer brings only their
// session cookie. Kept as a struct (not omitted) so the JSON
// decoder accepts a bare {} body without a "no JSON object"
// parse error. (Go's encoding/json decodes a missing body into
// the zero value of the request type, but the dashboard
// always sends an empty object — matching this keeps both
// sides happy.)
type MFAEnrollRequest struct{}

// MFAEnrollResponse is the one-shot enrollment payload. The
// customer sees Secret + RecoveryCodes exactly once; the
// server-side record holds only the sealed secret + SHA-256
// hashes. Re-calling /enroll returns a fresh secret + codes
// (the previous set is overwritten).
type MFAEnrollResponse struct {
	OTPAuthURL    string   `json:"otpauth_url"`
	Secret        string   `json:"secret"`
	QRCodePNG     []byte   `json:"qr_code_png_base64"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// MFAConfirmRequest is the body for /confirm. Totp is the
// 6-digit code from the customer's authenticator app.
//
// CSRFToken is the dashboard's CSRF token (issue #186 review
// Finding #7). The /confirm handler does NOT consume it —
// VerifyAuthenticated runs first and the token is absorbed
// from the body only for decodeJSON's DisallowUnknownFields
// check to pass. A drop of this field would surface as a 400
// "malformed JSON body" instead of a 403 "Invalid CSRF token",
// which would mask the real attack signal.
type MFAConfirmRequest struct {
	Totp      string `json:"totp"`
	CSRFToken string `json:"csrf_token,omitempty"`
}

// MFAVerifyRequest is the body for /verify. Same shape as
// /confirm but a separate type so the OpenAPI doc lists them
// separately. /verify is NOT CSRF-gated (the TOTP code IS
// the second factor), but the CSRFToken field is still
// absorbed so decodeJSON doesn't trip over the dashboard's
// always-set cookie.
type MFAVerifyRequest struct {
	Totp      string `json:"totp"`
	CSRFToken string `json:"csrf_token,omitempty"`
}

// MFADisableRequest is the body for /disable. Exactly one of
// Password / RecoveryCode is required; the handler validates
// the constraint and returns CodeValidation if neither or
// both are set.
type MFADisableRequest struct {
	Password     string `json:"password,omitempty"`
	RecoveryCode string `json:"recovery_code,omitempty"`
	CSRFToken    string `json:"csrf_token,omitempty"`
}

// MFARecoverRequest is the body for /recover.
type MFARecoverRequest struct {
	Code      string `json:"code"`
	CSRFToken string `json:"csrf_token,omitempty"`
}

// MFADisableEmailRequest starts the email-assisted MFA disable flow.
// The request has no business fields; CSRFToken is included for the
// browser mutation contract.
type MFADisableEmailRequest struct {
	CSRFToken string `json:"csrf_token,omitempty"`
}

// MFADisableEmailConfirmRequest carries the opaque one-time token from
// the email link. The token is consumed only after the cooldown elapses.
type MFADisableEmailConfirmRequest struct {
	Token     string `json:"token"`
	CSRFToken string `json:"csrf_token,omitempty"`
}

// All MFA success response types are deliberately empty bodies. They are
// deliberately empty — the meaningful side effects (cookie
// re-issue, audit Emit, store stamp) are not on the JSON wire.
// The handler returns 200 OK + a zero-byte body so the
// dashboard's "XHR succeeded → refresh the prompt" path
// branches on status alone.
type (
	MFAConfirmResponse             struct{}
	MFAVerifyResponse              struct{}
	MFADisableResponse             struct{}
	MFARecoverResponse             struct{}
	MFADisableEmailResponse        struct{}
	MFADisableEmailConfirmResponse struct{}
)
