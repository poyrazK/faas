package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
)

// hmacSHA256 returns a fresh HMAC-SHA256 keyed on secret, with key
// already written into the digest. Used as a one-liner wrapper
// around crypto/hmac to keep verifyPaddleSignature concise — the
// canonical-string format ("ts:body") and the comparison helper
// constantTimeEqual live next to the verifier.
//
// Wrapping rather than calling hmac.New directly makes the test
// surface easier to swap if a future Maintainability refactor
// prefers a sign-then-compare helper. Today the inlined version
// saves ~5 lines and a LocalAlloc per call.
func hmacSHA256(key, initial []byte) hashWriter {
	h := hmac.New(sha256.New, key)
	h.Write(initial)
	return h
}

// hashWriter is the slice-shaped surface crypto/hmac exposes after
// New(). Sum closes the digest; Write extends it. Pulled out as a
// named type so the call site reads cleanly without importing
// hash.Hash every time.
type hashWriter interface {
	Write(p []byte) (int, error)
	Sum(b []byte) []byte
}

// constantTimeEqual wraps crypto/hmac.Equal — exposed here rather
// than inlined so tests can substitute a non-constant-time compare
// for adversarial timing checks if needed (the OOTB production
// path is always constant-time).
func constantTimeEqual(a, b []byte) bool {
	return hmac.Equal(a, b)
}
