package auth

import (
	"errors"
	"strings"
	"testing"
)

// TestEncodeVerifyRoundTrip: Encode(password) followed by Verify(phc, password)
// returns (true, nil). The load-bearing happy path — every apid login and
// password-reset call is a round-trip on this pair.
func TestEncodeVerifyRoundTrip(t *testing.T) {
	cases := []string{
		"correct-horse-battery-staple",
		"another-valid-password-2026",
		strings.Repeat("a", MinPasswordLen), // exactly the floor
		strings.Repeat("x", 128),            // long
		"unicode-пароль-密码-🔐-ok",            // unicode; no rune-vs-byte split
	}
	for _, pw := range cases {
		t.Run(pw, func(t *testing.T) {
			phc, err := Encode(pw)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			// PHC sanity: starts with the Argon2id prefix.
			if !strings.HasPrefix(phc, "$argon2id$v=19$m=65536,t=1,p=2$") {
				t.Errorf("Encode PHC = %q, missing Argon2id prefix", phc)
			}
			ok, err := Verify(phc, pw)
			if err != nil {
				t.Fatalf("Verify(phc, same password): %v", err)
			}
			if !ok {
				t.Errorf("Verify(phc, same password) returned false; expected true")
			}
		})
	}
}

// TestEncodeFreshSalt: two Encode() calls on the same plaintext produce
// DIFFERENT PHC strings (because the salt is random). The hash output
// must be different even if the plaintext is identical — without
// per-call salt randomness, a leaked DB exposes precomputed rainbow
// tables and Argon2id's memory cost becomes worthless.
func TestEncodeFreshSalt(t *testing.T) {
	const pw = "correct-horse-battery-staple"
	a, err := Encode(pw)
	if err != nil {
		t.Fatalf("first Encode: %v", err)
	}
	b, err := Encode(pw)
	if err != nil {
		t.Fatalf("second Encode: %v", err)
	}
	if a == b {
		t.Errorf("two Encode() calls on the same plaintext produced identical PHC strings; salt is not random")
	}
	// Both must still verify against the original plaintext.
	for _, phc := range []string{a, b} {
		ok, err := Verify(phc, pw)
		if err != nil || !ok {
			t.Errorf("Verify(%q, %q) = (%v, %v); want (true, nil)", phc, pw, ok, err)
		}
	}
}

// TestVerifyRejectsWrongPassword: Verify(phc, "wrong") returns
// (false, nil). The (false, nil) shape — not (false, err) — is
// load-bearing: the apid handler maps (false, nil) to 401
// invalid_credentials, and (false, err) to 500. Mixing them up
// would either lock out valid users or mask DB corruption.
func TestVerifyRejectsWrongPassword(t *testing.T) {
	phc, err := Encode("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	ok, err := Verify(phc, "wrong-password-also-long")
	if err != nil {
		t.Errorf("Verify on wrong password returned err = %v, want nil (mismatch is not a DB error)", err)
	}
	if ok {
		t.Errorf("Verify on wrong password returned true; expected false")
	}
}

// TestVerifyRejectsMalformedPHC: Verify must return (false, err) for
// every garbage input we can throw at it. The "false without err"
// path is reserved for "wrong password, real row", and "false with
// err" is reserved for "the row is corrupt, refuse sign-in".
//
// The test pins every shape we know how to construct: wrong field
// count, wrong algorithm, wrong version, malformed params, bad
// base64 in salt and hash. A future refactor of parsePHC that drops
// a guard surfaces here.
func TestVerifyRejectsMalformedPHC(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no_dollar_prefix": "argon2id$v=19$m=65536,t=1,p=2$AAAA$BBBB",
		"too_few_fields":   "$argon2id$v=19$m=65536,t=1,p=2$AAAA",
		// "too_many_fields" — split() on "$" yields 7 elements
		// (one leading empty + 6 non-empty), which is the right
		// number for the format this package emits. To trigger the
		// "too many" branch we need a 7th non-empty field, so the
		// split has length 8.
		"too_many_fields":  "$argon2id$v=19$m=65536,t=1,p=2$AAAA$BBBB$CCCC$DDDD",
		"wrong_algorithm":  "$bcrypt$v=19$m=65536,t=1,p=2$AAAA$BBBB",
		"wrong_version":    "$argon2id$v=99$m=65536,t=1,p=2$AAAA$BBBB",
		"params_missing_m": "$argon2id$v=19$t=1,p=2$AAAA$BBBB",
		"params_zero_m":    "$argon2id$v=19$m=0,t=1,p=2$AAAA$BBBB",
		"params_zero_t":    "$argon2id$v=19$m=65536,t=0,p=2$AAAA$BBBB",
		"params_zero_p":    "$argon2id$v=19$m=65536,t=1,p=0$AAAA$BBBB",
		"params_huge_p":    "$argon2id$v=19$m=65536,t=1,p=99999$AAAA$BBBB",
		"salt_invalid_b64": "$argon2id$v=19$m=65536,t=1,p=2$%%%%$BBBB",
		"hash_invalid_b64": "$argon2id$v=19$m=65536,t=1,p=2$AAAA$%%%%",
		// An empty hash must not verify every password.
		"hash_empty": "$argon2id$v=19$m=65536,t=1,p=2$AAAA$",
		"salt_empty": "$argon2id$v=19$m=65536,t=1,p=2$$KKo93VKUFEZKsJPb2ovaTfi0MbZQdU4EWw7DjfV9j1c",
	}
	for name, phc := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := Verify(phc, "any-password-long-enough")
			if err == nil {
				t.Errorf("Verify(%q, ...) returned err=nil; want ErrMalformedPHC", phc)
			}
			if ok {
				t.Errorf("Verify(%q, ...) returned ok=true; want false on malformed input", phc)
			}
			if !errors.Is(err, ErrMalformedPHC) {
				t.Errorf("Verify(%q, ...) err = %v, want errors.Is(_, ErrMalformedPHC)", phc, err)
			}
		})
	}
}

// TestValidate enforces the NIST-style floor (MinPasswordLen). The
// short-password path must return ErrPasswordTooShort, distinct from
// the malformed-PHC path so the apid error handler maps to
// CodePasswordTooWeak (RFC 7807 400) rather than the verify error.
func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"empty", "", false},
		{"one_short", "a", false},
		{"one_below_floor", strings.Repeat("a", MinPasswordLen-1), false},
		{"exactly_floor", strings.Repeat("a", MinPasswordLen), true},
		{"above_floor", strings.Repeat("a", MinPasswordLen+1), true},
		{"long", strings.Repeat("a", 128), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.pw)
			if c.ok && err != nil {
				t.Errorf("Validate(%q) = %v, want nil", c.pw, err)
			}
			if !c.ok && !errors.Is(err, ErrPasswordTooShort) {
				t.Errorf("Validate(%q) = %v, want ErrPasswordTooShort", c.pw, err)
			}
		})
	}
}

// TestEncodeRejectsShort: Encode and Validate share the floor.
// Encode must NOT spend the Argon2id cost on a password that's
// going to be rejected anyway — the caller wants ErrPasswordTooShort
// back before the CPU work, so a 1-char "x" returns in <1 ms rather
// than 50 ms.
func TestEncodeRejectsShort(t *testing.T) {
	for _, pw := range []string{"", "short", strings.Repeat("a", MinPasswordLen-1)} {
		_, err := Encode(pw)
		if !errors.Is(err, ErrPasswordTooShort) {
			t.Errorf("Encode(%q) err = %v, want ErrPasswordTooShort", pw, err)
		}
	}
}

// TestDummyPHCVerifies pins the anti-enumeration pad. The fixed
// dummy hash must be a valid Argon2id PHC that Verify accepts as
// input — otherwise the postLogin no-account branch panics or
// returns ErrMalformedPHC, which would silently flip from a 401 to
// a 500 and re-open the timing oracle (the all-good path runs in
// ~50 ms; the error path runs in <1 ms; the timing channel returns).
//
// The dummy plaintext is intentionally not derivable from the
// hash, so we test that Verify(DummyPHC, anything) returns
// (false, nil) — i.e. "no match, but the input parsed". A real
// attacker cannot make the pad return true because they don't
// know the underlying plaintext.
func TestDummyPHCVerifies(t *testing.T) {
	if !strings.HasPrefix(DummyPHC, "$argon2id$v=19$m=65536,t=1,p=2$") {
		t.Errorf("DummyPHC missing Argon2id prefix; the anti-enumeration pad in handlers_auth_login.go would reject it as malformed: %q", DummyPHC)
	}
	// Whatever plaintext the attacker supplies, the pad must NOT match.
	ok, err := Verify(DummyPHC, "anything-the-attacker-types")
	if err != nil {
		t.Errorf("Verify(DummyPHC, ...) err = %v; the pad must parse the dummy hash without error", err)
	}
	if ok {
		t.Errorf("Verify(DummyPHC, ...) ok = true; an attacker-supplied plaintext matched the pad hash — DummyPHC must be a real Argon2id hash of a secret plaintext, not a constant")
	}
	// Same plaintext, real Encode: should also not match the pad
	// (the salt is different so the output bytes differ).
	phc, err := Encode("anything-the-attacker-types")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	ok, err = Verify(phc, "anything-the-attacker-types")
	if err != nil || !ok {
		t.Errorf("Encode+Verify round-trip on real hash broken: ok=%v err=%v", ok, err)
	}
}

// TestFormatParseRoundTrip pins the PHC wire format. Encode produces
// a string, parsePHC must accept it back. We don't expose parsePHC
// (it's unexported), but Verify uses it on every call — a regression
// in formatPHC would surface as Verify failing on its own output,
// which the round-trip test catches.
func TestFormatParseRoundTrip(t *testing.T) {
	phc, err := Encode("round-trip-password-2026")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	ok, err := Verify(phc, "round-trip-password-2026")
	if err != nil || !ok {
		t.Errorf("Encode+Verify round-trip broken: ok=%v err=%v (PHC = %q)", ok, err, phc)
	}
}
