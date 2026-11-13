package journeys

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156. The deployed journey proves a real API response can be
// correlated; the isolated journey controls errors and credential redaction.
func TestHTTPDebugLiveAPI(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	r := tg.ok(t, time.Minute, "--debug-http", "billing", "usage", "summary")
	objects(t, r.stdout)
	assertHTTPDebugPairs(t, r.stderr)
}

func TestHTTPDebugIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	const id = "12345678123412341234123456789abc"
	var status, requests atomic.Int32
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/usage/summary" || r.Header.Get("Authorization") != "Bearer fp_http-debug-private" {
			t.Error("debug command changed API target or authentication")
		}
		w.Header().Set("X-Faas-Request-Id", id)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "cookie-private")
		code := int(status.Load())
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = io.WriteString(w, `{}`)
		} else {
			_, _ = io.WriteString(w, `{"status":404,"title":"Not found","detail":"no deployment","code":"not_found"}`)
		}
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	for _, code := range []int{http.StatusOK, http.StatusNotFound} {
		status.Store(int32(code))
		before := requests.Load()
		plain := tg.exec(t, "fp_http-debug-private", tg.home, time.Minute, "billing", "usage", "summary")
		debug := tg.exec(t, "fp_http-debug-private", tg.home, time.Minute, "--debug-http", "billing", "usage", "summary")
		if plain.exit != debug.exit || !bytes.Equal(plain.stdout, debug.stdout) || requests.Load()-before != 2 {
			t.Fatalf("debug changed command result or number of requests: plain exit %d, debug exit %d", plain.exit, debug.exit)
		}
		if code == http.StatusOK && plain.exit != 0 || code == http.StatusNotFound && plain.exit == 0 {
			t.Fatalf("fixture returned an unexpected command exit: %d", plain.exit)
		}
		if !bytes.HasSuffix(debug.stderr, plain.stderr) {
			t.Fatal("debug changed ordinary command error output")
		}
		trace := bytes.TrimSuffix(debug.stderr, plain.stderr)
		assertHTTPDebugPairs(t, trace)
		for _, private := range []string{server.URL, "fp_http-debug-private", "cookie-private"} {
			if bytes.Contains(trace, []byte(private)) {
				t.Fatal("HTTP diagnostic leaked URL or credentials")
			}
		}
	}
}

func assertHTTPDebugPairs(t *testing.T, stderr []byte) {
	t.Helper()
	seen := map[int][]string{}
	for _, line := range bytes.Split(bytes.TrimSpace(stderr), []byte("\n")) {
		var row struct {
			Type    string `json:"type"`
			Version int    `json:"schema_version"`
			Seq     int    `json:"request_seq"`
			Event   string `json:"event"`
			Route   string `json:"route"`
			Status  int    `json:"status_code"`
			Elapsed *int64 `json:"headers_ms"`
		}
		if json.Unmarshal(line, &row) != nil || row.Type != "http_debug" {
			continue // Existing command warnings and errors may share stderr.
		}
		if row.Version != 1 || row.Seq < 1 || !strings.HasPrefix(row.Route, "/") {
			t.Fatal("invalid diagnostic envelope")
		}
		if row.Event == "response_headers" && (row.Status < 100 || row.Elapsed == nil || *row.Elapsed < 0) {
			t.Fatal("missing response metadata")
		}
		seen[row.Seq] = append(seen[row.Seq], row.Event)
	}
	if len(seen) == 0 {
		t.Fatal("no HTTP diagnostic records")
	}
	for _, events := range seen {
		if len(events) != 2 || events[0] != "request_start" || events[1] != "response_headers" {
			t.Fatalf("unpaired HTTP diagnostics: %v", events)
		}
	}
}
