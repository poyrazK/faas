package paddle

import "strings"

// redactedKeyMarker is the fallback string emitted by redactAPIKey
// when the input is too short to truncate to the first 4 chars.
// Same value pkg/mail uses for outgoing-provider redaction
// (CLAUDE.md "never log secret values"). Pulled out as a constant
// so the goconst linter sees one occurrence instead of three.
const redactedKeyMarker = "***"

// cloneBytes returns a defensive copy of b. The webhook parser
// stores the raw payload on the returned Event so apid's audit
// log can quote it later; a defensive copy prevents a downstream
// caller mutating the slice from corrupting the verifier's view.
//
// Equivalent to bytes.Clone; preferred here so the package stays
// at zero stdlib version-gates — paddle/ may be imported by tools
// (clid, builderd) that don't share the SDK's Go-version baseline.
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
//
// Called from provider.go (init-failure log line) and via the
// `package paddle` test in util_test.go. No public re-export — the
// test reaches the lower-case identifier directly.
func redactAPIKey(key string) string {
	if len(key) <= 4 {
		return redactedKeyMarker
	}
	return key[:4] + strings.Repeat("*", len(key)-4)
}
