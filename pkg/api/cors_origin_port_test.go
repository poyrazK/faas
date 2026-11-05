package api

import "testing"

// A port wildcard admits a numeric port only; anything else after the colon
// is not an origin a browser sends and must not match.
func TestMatchEdgeRuleCORSOriginPortWildcardIsNumeric(t *testing.T) {
	allow := []string{"https://localhost:*"}
	for origin, want := range map[string]string{
		"https://localhost:3000":          "https://localhost:3000",
		"https://localhost:65535":         "https://localhost:65535",
		"https://localhost:3000.evil.com": "",
		"https://localhost:":              "",
		"https://localhost:123456":        "",
		"https://localhost:80x":           "",
		"http://localhost:3000":           "",
	} {
		if got := MatchEdgeRuleCORSOrigin(allow, origin); got != want {
			t.Errorf("%q: got %q, want %q", origin, got, want)
		}
	}
}
