package wire_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// ADR-20261023200002: the profiling handler serves the runtime profiles,
// bounds CPU profiles and traces, and never serves cmdline.
func TestDebugProfileHandler(t *testing.T) {
	srv := httptest.NewServer(wire.DebugProfileHandler())
	defer srv.Close()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/debug/pprof/", 200, "goroutine"},
		{"/debug/pprof/heap?debug=1", 200, "heap profile"},
		{"/debug/pprof/goroutine?debug=2", 200, "goroutine "},
		{"/debug/pprof/allocs", 200, ""},
		{"/debug/pprof/heap?debug=3", 400, "debug must be"},
		{"/debug/pprof/cmdline", 404, ""},
		{"/debug/pprof/symbol", 404, ""},
		{"/debug/pprof/profile?seconds=61", 400, "seconds must be 1..60"},
		{"/debug/pprof/profile?seconds=0", 400, "seconds must be"},
		{"/debug/pprof/trace?seconds=abc", 400, "seconds must be"},
	} {
		status, body := get(tc.path)
		if status != tc.status || !strings.Contains(body, tc.contains) {
			t.Errorf("GET %s = %d %.80q, want %d containing %q", tc.path, status, body, tc.status, tc.contains)
		}
	}

	// A one-second CPU profile is a gzip-compressed protobuf.
	status, body := get("/debug/pprof/profile?seconds=1")
	if status != 200 || !bytes.HasPrefix([]byte(body), []byte{0x1f, 0x8b}) {
		t.Fatalf("CPU profile = %d, %d bytes; want gzip", status, len(body))
	}
}

func TestDebugProfilingKillSwitchAndNames(t *testing.T) {
	for value, want := range map[string]bool{"": true, "on": true, "off": false, "OFF": false, "false": false, "0": false, "no": false} {
		if got := wire.DebugProfilingEnabled(func(string) string { return value }); got != want {
			t.Errorf("FAAS_DEBUG_PROFILING=%q enabled = %v, want %v", value, got, want)
		}
	}
	for name, want := range map[string]bool{"schedd": true, "gatewayd-internal": true, "apid.1234": true, "": false, "a/b": false, "a b": false, strings.Repeat("x", 65): false} {
		if got := wire.ValidDebugProfileName(name); got != want {
			t.Errorf("ValidDebugProfileName(%q) = %v, want %v", name, got, want)
		}
	}
}
