// Package auth holds primitives shared across the dashboard-side auth
// surface (cmd/apid/handlers_auth_*.go). PR #2 (issue #165 / ADR-032)
// lands email+password as the primary credential; the Argon2id encoder
// and verifier live here so every caller agrees on the PHC wire format
// and the parameters that produced a given hash.
//
// Why Argon2id:
//
//   - OWASP's recommendation for new systems as of 2024; bcrypt has no
//     memory-hardness knob and is increasingly easy to farm out to
//     GPU/ASIC rigs.
//   - The CPU cost on the auth path (one verify per login, ~50 ms on
//     the EX44 with m=64MiB / t=1 / p=2) is negligible against the
//     10/min/IP spec §11 rate-limit ceiling.
//
// Why PHC string format:
//
//   - The hash embeds the Argon2id parameters (memory, time, threads)
//     and the salt inline, so a future "bump to memory=128MiB" rollout
//     is a no-op migration: existing rows verify with the OLD params,
//     new rows carry the NEW ones. Verify parses m/t/p out of the
//     stored hash rather than reading package constants, so two rows
//     on the same table can carry different parameter sets without
//     coordination.
//
// Why a side table rather than a column on accounts:
//
//   - Most accounts will be OAuth-only (Google + GitHub bind a sub +
//     email_verified=true with no password ever set). A nullable text
//     column on accounts means every SELECT * and every json.Marshal
//     of Account carries a field that's null 90%+ of the time. A side
//     table is read only when needed (cmd/apid/handlers_auth_login.go).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameter defaults per spec §11 (issue #165 / ADR-032).
// These are the values a brand-new Encode() call embeds in the PHC
// string. A future parameter bump changes the defaults; existing
// hashes verify with whatever parameters they were encoded with.
const (
	// Memory is the KiB cost per hash. 64 MiB = 65,536 KiB.
	argonMemoryKiB uint32 = 64 * 1024
	// Time is the iteration count. OWASP recommends t≥1; t=1 with
	// m=64MiB is ~50 ms on the EX44 control-plane cores.
	argonTime uint32 = 1
	// Threads is the lane count for Argon2id's internal
	// parallel-pass. p=2 is a common balance between throughput and
	// memory-bandwidth saturation on small (≤4-core) hosts.
	argonThreads uint8 = 2

	// SaltLen is the per-hash random salt size. 16 bytes (128 bits)
	// matches the OWASP minimum and is large enough that salt reuse
	// across two accounts is astronomically unlikely.
	saltLen = 16
	// KeyLen is the derived-key length. 32 bytes (256 bits) matches
	// the SHA-256 output size and is what `argon2.IDKey` produces by
	// default. The output length is also embedded in the PHC string
	// so Verify can re-derive the same length without trusting
	// caller-side hints.
	keyLen = 32
)

// MinPasswordLen is the NIST-style floor enforced at signup / reset.
// No complexity rules (no "must contain a digit", no character-class
// scoring) — modern NIST 800-63B guidance treats length as the
// load-bearing axis and discourages composition rules for their
// negative impact on memorability and reuse.
//
// Why 12: at the OWASP 2024 reference rate (~1e10 guesses/sec for a
// offline attack on Argon2id), 12 chars of full ASCII gives ~80 bits
// of entropy and survives the offline attack budget for the lifetime
// of the account.
const MinPasswordLen = 12

// ErrPasswordTooShort is returned by Validate when the plaintext is
// below MinPasswordLen. Distinct from the encoded-hash path so the
// apid error handler can map it to the CodePasswordTooWeak RFC 7807
// response with a precise Detail ("password must be at least 12
// characters").
var ErrPasswordTooShort = errors.New("auth: password too short")

// ErrMalformedPHC is returned by Verify when the stored hash doesn't
// parse as an Argon2id PHC string. Treat as a hard error: the row in
// the DB is corrupt or was written by a different encoder, and
// refusing to fall back to "true" or "false" is the only safe move.
var ErrMalformedPHC = errors.New("auth: malformed PHC string")

// Encode hashes plaintext with Argon2id using the package's default
// parameters and returns the PHC string for storage.
//
// The salt is generated via crypto/rand and is never reused. The
// returned PHC string is self-describing:
//
//	$argon2id$v=19$m=65536,t=1,p=2$<salt-b64>$<hash-b64>
//
// Verify parses the embedded m/t/p out of the stored string rather
// than reading package constants, so a future parameter bump does
// not break old hashes.
func Encode(plaintext string) (string, error) {
	if err := Validate(plaintext); err != nil {
		return "", err
	}
	salt, err := randomBytes(saltLen)
	if err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	hash := argon2.IDKey([]byte(plaintext), salt, argonTime, argonMemoryKiB, argonThreads, keyLen)
	return formatPHC(argonMemoryKiB, argonTime, argonThreads, salt, hash), nil
}

// Validate returns ErrPasswordTooShort when plaintext is below
// MinPasswordLen. Caller-side check so the apid signup / set-password
// paths can map the failure to CodePasswordTooWeak without paying
// the Argon2id cost first.
func Validate(plaintext string) error {
	if len(plaintext) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	return nil
}

// Verify recomputes the Argon2id hash from the stored PHC string and
// compares it against the supplied plaintext in constant time.
//
// Returns:
//
//	(true,  nil)  — match.
//	(false, nil)  — mismatch (the stored hash was produced by this
//	                 package, the password is wrong).
//	(false, err)  — malformed PHC (DB corruption or a foreign
//	                 encoder). The caller MUST treat this as a hard
//	                 error and surface it; the safe default for
//	                 "can't decide" is to refuse sign-in, not to
//	                 fall back to "false".
//
// Anti-enumeration: the constant-time pad on the no-account branch
// (cmd/apid/handlers_auth_login.go:postLogin) is built on top of
// this function — Verify against a fixed dummy PHC takes the same
// ~50 ms whether or not the real account exists, closing the timing
// oracle that the old `if AccountByEmail != nil` check opened.
func Verify(phc, plaintext string) (bool, error) {
	mem, time, threads, salt, want, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plaintext), salt, time, mem, threads, uint32(len(want)))
	// subtle.ConstantTimeCompare returns 1 iff lengths match AND
	// every byte is equal. Length mismatch is the natural result of
	// a truncated hash and the comparison correctly reports 0.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, nil
	}
	return true, nil
}

// DummyPHC is a fixed, never-rotated Argon2id hash used by the
// anti-enumeration pad in cmd/apid/handlers_auth_login.go:postLogin.
// On the no-account / no-password-row branch, the handler calls
// Verify(DummyPHC, plaintext) before returning 401 so the response
// time on a known email + wrong password matches the response time
// on an unknown email + wrong password — closing the timing oracle.
//
// The PHC is generated by Encode() with the same defaults so the cost
// is identical to a real verify; the underlying plaintext is a
// secret string that nothing in the system ever submits. An attacker
// who knows DummyPHC still can't recover the plaintext without
// re-running Argon2id, and even if they did, the pad's job is only
// to equalise timing — not to keep the plaintext secret.
//
// The string is a const so the package doesn't pay for a one-shot
// Encode at init time (Argon2id is ~50 ms; running it in init would
// freeze every binary that imports pkg/auth on startup, including
// the unit tests that don't need the pad). The hash was generated
// once via Encode("antipad-not-a-real-password-32chars") and pasted
// here; the plaintext is not derivable from the hash without
// re-running Argon2id.
const DummyPHC = "$argon2id$v=19$m=65536,t=1,p=2$IWe/FcOEMwkECtSQIvrVzQ$KKo93VKUFEZKsJPb2ovaTfi0MbZQdU4EWw7DjfV9j1c"

// randomBytes is split out so the test in password_test.go can swap
// in a deterministic source if a future test ever wants to pin the
// salt (today we don't, because every Encode() must produce a fresh
// salt to retain its security property).
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// formatPHC renders the Argon2id PHC string. Inline rather than via
// a struct because the format is part of the wire contract — the
// pgstore round-trips this string verbatim into the account_passwords
// table, and Verify parses it back into the same five fields. A
// helper struct would obscure the on-disk shape.
func formatPHC(mem uint32, time uint32, threads uint8, salt, hash []byte) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		mem, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}

// parsePHC is the inverse of formatPHC. It rejects malformed input
// at the first unrecognised component — there's no point trying to
// "be lenient" with a stored hash; either the row was written by
// this package or the row is corrupt.
func parsePHC(phc string) (mem uint32, time uint32, threads uint8, salt, hash []byte, err error) {
	// PHC layout: $argon2id$v=19$m=65536,t=1,p=2$<salt>$<hash>
	// Splitting on "$" yields a leading empty string (the slice
	// starts with the leading $) followed by five non-empty fields,
	// so the slice has length 6. The "$" separator is a single
	// byte; argon's spec allows extra fields in newer versions but
	// v=19 has exactly five after the leading $.
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: wrong field count (%d)", ErrMalformedPHC, len(parts))
	}
	if parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: alg=%q, want argon2id", ErrMalformedPHC, parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: version=%q", ErrMalformedPHC, parts[2])
	}
	var (
		m uint32
		t uint32
		p uint32
	)
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: params=%q", ErrMalformedPHC, parts[3])
	}
	if m == 0 || t == 0 || p == 0 || p > 255 {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: params out of range (m=%d t=%d p=%d)", ErrMalformedPHC, m, t, p)
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("salt b64: %w", errors.Join(ErrMalformedPHC, err))
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("hash b64: %w", errors.Join(ErrMalformedPHC, err))
	}
	// An empty hash would make Verify accept any password:
	// ConstantTimeCompare reports two empty slices as equal.
	if len(salt) == 0 || len(hash) < 16 {
		return 0, 0, 0, nil, nil, fmt.Errorf("%w: salt=%d hash=%d bytes", ErrMalformedPHC, len(salt), len(hash))
	}
	return m, t, uint8(p), salt, hash, nil
}
