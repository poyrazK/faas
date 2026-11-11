package fcvm

import (
	"testing"
	"time"
)

// adr: 20261027090013 step 10 — an image session's sandbox.json is the
// version-1 marker, or version 2 when a template brings commands.
func TestSandboxManifest(t *testing.T) {
	for _, tc := range []struct {
		start, ready string
		timeout      time.Duration
		want         string
	}{
		{"", "", 0, `{"kind":"sandbox","version":1}`},
		{"serve", "", time.Minute, `{"kind":"sandbox","version":2,"start_cmd":"serve"}`},
		{"serve", "probe", 90 * time.Second, `{"kind":"sandbox","version":2,"start_cmd":"serve","ready_cmd":"probe","ready_timeout_ms":90000}`},
	} {
		got, err := sandboxManifest(tc.start, tc.ready, tc.timeout)
		if err != nil || string(got) != tc.want {
			t.Errorf("sandboxManifest(%q, %q, %v) = %s, %v; want %s", tc.start, tc.ready, tc.timeout, got, err, tc.want)
		}
	}
}
