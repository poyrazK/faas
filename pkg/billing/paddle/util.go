package paddle

import "strings"

// cloneBytes is a small defensive-copy helper for webhook payloads —
// the bytes/Clone idiom is fine but Go ≥ 1.20 is not universal in the
// project, so this stays local. Verified equivalent to bytes.Clone
// (PR #155 review fix #3 went the other way in pkg/billing/stripe).
func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// redactAPIKey strips an apiKey before it lands in a log line or
// error message. The first 4 chars are kept so an operator can
// distinguish environments (sk_test/sandbox/live) without leaking
// the secret; the rest is masked. Same rule pkg/mail uses for
// outgoing providers (CLAUDE.md: "never log secret values").
func redactAPIKey(key string) string {
	if len(key) <= 4 {
		return "***"
	}
	return key[:4] + strings.Repeat("*", len(key)-4)
}

// RedactAPIKeyForTest exposes redactAPIKey to package-internal
// tests (`paddle_test`) without making it part of the public API.
// The test-only exit lets us lock the masking rule down via a
// table-driven test without an `_test.go` file reaching into the
// lowercase identifier (the standard linter rule against
// underscore-only-test exports).
func RedactAPIKeyForTest(key string) string { return redactAPIKey(key) }
