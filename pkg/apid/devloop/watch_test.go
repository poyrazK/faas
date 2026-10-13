package devloop

import (
	"strings"
	"testing"
)

// adr: 1170 — only a plain lowercase hostname reaches the watch-mode image;
// anything else is dropped rather than failing the session.
func TestWatchHost(t *testing.T) {
	for in, want := range map[string]string{
		"dev-api.gregale.dev":       "dev-api.gregale.dev",
		" Dev-API.Gregale.dev. ":    "dev-api.gregale.dev",
		"":                          "",
		"a.example,evil.example":    "",
		"bad host.example":          "",
		"\"quoted\".example":        "",
		strings.Repeat("a", 254):    "",
		strings.Repeat("a", 253):    strings.Repeat("a", 253),
		"xn--bcher-kva.example.com": "xn--bcher-kva.example.com",
	} {
		if got := WatchHost(in); got != want {
			t.Errorf("WatchHost(%q) = %q, want %q", in, got, want)
		}
	}
}
