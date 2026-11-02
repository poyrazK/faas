package api

import "testing"

// A stored drain or callback credential may follow an update only to the
// same scheme and host (bug hunt 2026-10-28 H107).
func TestSameURLOrigin(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"https://logs.example.com/ingest", "https://logs.example.com/v2/ingest?x=1", true},
		{"https://Logs.Example.com/a", "https://logs.example.com/b", true},
		{"https://logs.example.com/a", "https://attacker.example/a", false},
		{"https://logs.example.com/a", "http://logs.example.com/a", false},
		{"https://logs.example.com/a", "https://logs.example.com:8443/a", false},
		{"https://logs.example.com/a", "not a url", false},
		{"", "https://logs.example.com/a", false},
	} {
		if got := SameURLOrigin(tc.a, tc.b); got != tc.want {
			t.Errorf("SameURLOrigin(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
