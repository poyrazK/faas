package gateway

import (
	"strings"
	"testing"
)

// TestSanitizeLogField locks the contract for CodeQL's go/log-injection
// (CWE-117) defense on the synth RPC log fields. The helper strips
// ASCII control characters so a malicious producer cannot forge log
// lines via CR/LF injection; tabs survive because slog's JSON encoder
// and most log readers treat them as whitespace.
func TestSanitizeLogField(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"plain passes through", "hello-world_42.app", "hello-world_42.app"},
		{"tab survives", "a\tb", "a\tb"},
		{"newline replaced", "a\nb", "a·b"},
		{"carriage return replaced", "a\rb", "a·b"},
		{"null replaced", "a\x00b", "a·b"},
		{"del replaced", "a\x7fb", "a·b"},
		{"unicode passes through", "héllo·世界", "héllo·世界"},
		{"multiple controls", "x\ny\rz\x00", "x·y·z·"},
		{"only controls", "\n\r\x00", "···"},
		{"path-style survives", "/v1/synthesize?foo=bar", "/v1/synthesize?foo=bar"},
		{"quoted path survives", `/foo/"bar"`, `/foo/"bar"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeLogField(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeLogField(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Belt + suspenders: no CR or LF ever leaves the helper.
			if strings.ContainsAny(got, "\r\n") {
				t.Errorf("sanitizeLogField(%q) leaked CR/LF: %q", tc.in, got)
			}
		})
	}
}
