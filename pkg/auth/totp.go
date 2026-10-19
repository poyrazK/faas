// Package auth: TOTP primitives (IAM-2 / issue #186).
//
// The TOTP secret is generated here, rendered into a QR for the
// dashboard via pkg/auth/totp.QRCode, and verified on every login
// step-up. The wire shape for verification is pkg/auth/totp.VerifyCode —
// a fixed-window RFC 6238 check (SHA-1, 6 digits, 30 s period, ±1 step
// skew). The skew is tighter than the pquerna/otp default (which is 0)
// because the customer-driven ±30 s covers the typical "I just typed
// the code" latency without admitting brute-force padding.
//
// Why SHA-1 (not SHA-256 / SHA-512):
//
//   - RFC 6238 mandates SHA-1 as the reference algorithm. Authenticator
//     apps (Google Authenticator, Authy, 1Password, Bitwarden) all
//     validate SHA-1. A SHA-256 TOTP code would not interoperate.
//   - The secret is 160 bits of fresh os.GetRandom entropy; the hash
//     algorithm is the second-stage compression, not the bottleneck.
//     brute-force resistance is a property of the secret length, not
//     the hash function.
//
// Why ±1 step (not 0):
//
//   - 0-step require the customer's clock to be within ±0 s of the
//     server's, which is unrealistic on phones with NTP drift. ±1
//     step (±30 s) covers the 99th-percentile latency from "I read
//     the code" to "the server validates it" without admitting more
//     than 3 verify attempts per 30 s window from the same code.
//
// Recovery codes are a separate concern. SHA-256 (not Argon2id)
// because the codes carry their own entropy: 50 bits per code (10
// chars × 5 bits/char over the customer-visible base32 alphabet
// A-Z + 2-7), from an 80-bit CSPRNG source (10 random bytes) that
// is base32-encoded and truncated to the 10 visible characters.
// Argon2id's cost is unjustified at this entropy floor — the
// per-verify work is bounded at 10 SHA-256 compares against the
// stored hash slice, which is negligible against the cold-boot
// wake budget the rest of the platform budgets.

package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPIssuer is stamped into the otpauth URL so the customer's
// authenticator app groups the account under "FaaS". Keep it short
// — RFC 6238 doesn't constrain the issuer length, but every
// authenticator app truncates the URL display and a long issuer
// steals chars from the AccountName field.
const TOTPIssuer = "FaaS"

// Recovery-code primitives moved to pkg/authcode so pkg/state's
// tests can use them without closing an import cycle (pkg/auth
// imports pkg/state since the middleware lift, ADR-044).
//
// New code MUST import pkg/authcode directly. The pkg/auth package
// no longer re-exports RecoveryCodeCount / NewRecoveryCodes /
// HashRecoveryCode — cmd/apid/handlers_mfa.go has been updated.

// GenerateSecret returns a fresh 160-bit base32-encoded secret. The
// returned string has base32 padding stripped (RFC 4648 §3.5) so the
// secret fits inside the otpauth URL's `secret=` query parameter
// without URL-escaping the `=` characters. Re-mint on every
// /enroll call; the secret is overwritten on the account row by
// SetMFASecret so the previous one is dead by the time the new one
// is issued.
func GenerateSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth/totp: rand: %w", err)
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="), nil
}

// KeyFromSecret builds the *otp.Key used for QR + otpauth URL
// rendering. The account email is the label so the customer's
// authenticator app groups the entry under "FaaS · alice@example.com".
// The same key is rendered as a PNG via QRCode and as the otpauth
// URL exposed in the MFAEnrollResponse so the customer can copy
// either form.
func KeyFromSecret(secret, accountEmail string) (*otp.Key, error) {
	// GenerateOpts.Secret takes the raw key bytes and base32-encodes
	// them into the URL; passing the base32 text itself would make the
	// QR carry a different key than VerifyCode checks.
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return nil, fmt.Errorf("auth/totp: decode secret: %w", err)
	}
	return totp.Generate(totp.GenerateOpts{
		Issuer:      TOTPIssuer,
		AccountName: accountEmail,
		Secret:      raw,
		// otp.DigitsSix + otp.AlgorithmSHA1 are the pquerna/otp
		// defaults; explicit here so a future default flip doesn't
		// silently de-link existing enrollments.
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
}

// QRCode returns the PNG bytes of the otpauth QR for the dashboard's
// /v1/account/mfa/enroll response. 256x256 covers typical phone
// screens at 2x or 3x DPR; the dashboard renders it at the same
// logical size as the recovery-code list. pquerna/otp's Image method
// returns the Go image.Image interface; we encode to PNG here so
// the handler can write the bytes directly to the response body.
func QRCode(key *otp.Key) ([]byte, error) {
	img, err := key.Image(256, 256)
	if err != nil {
		return nil, fmt.Errorf("auth/totp: qr image: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("auth/totp: png encode: %w", err)
	}
	return buf.Bytes(), nil
}

// VerifyCode returns true when code matches the secret at the
// current 30-second step, allowing ±1 step of skew. Strict default
// for IAM-2: the customer's clock is roughly synced (every modern
// phone does NTP), and a tighter skew tightens the brute-force
// envelope. The verify path returns ok/false from pquerna/otp
// without an error wrapper because the only error path is
// malformed-code (length-mismatch), which surfaces as a definitive
// false to the caller.
func VerifyCode(secret, code string) bool {
	ok, err := totp.ValidateCustom(code, secret, time.Now().UTC(),
		totp.ValidateOpts{
			Period:    30,
			Skew:      1,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
	return err == nil && ok
}
