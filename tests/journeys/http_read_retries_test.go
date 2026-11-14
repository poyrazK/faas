package journeys

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: explicit retries on a read-only billing API request.
func TestHTTPReadRetriesLiveAPI(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	objects(t, tg.ok(t, 2*time.Minute, "--http-retries", "1", "billing", "usage", "summary").stdout)
}

func TestHTTPReadRetriesIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	var requests, failures atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/usage/summary" || r.Header.Get("Authorization") != "Bearer fp_retries-private" {
			t.Error("retry changed API target or credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "cookie-private")
		if failures.Add(-1) >= 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"service_unavailable","detail":"body-private"}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	plain := tg.exec(t, "fp_retries-private", tg.home, time.Minute, "billing", "usage", "summary")
	if plain.exit != 0 {
		t.Fatalf("baseline failed: %s", plain.stderr)
	}
	for _, prefix := range [][]string{{"--http-retries", "1"}, {"--http-retries=1", "--debug-http"}} {
		failures.Store(1)
		before := requests.Load()
		args := append(append([]string{}, prefix...), "billing", "usage", "summary")
		result := tg.exec(t, "fp_retries-private", tg.home, time.Minute, args...)
		if result.exit != 0 || !bytes.Equal(result.stdout, plain.stdout) || requests.Load()-before != 2 {
			t.Fatalf("read retry changed stdout or request budget: exit=%d stderr=%s", result.exit, result.stderr)
		}
		if len(prefix) > 1 && prefix[1] == "--debug-http" {
			assertHTTPDebugPairs(t, result.stderr)
			if !bytes.Contains(result.stderr, []byte(`"attempt":2`)) || !bytes.Contains(result.stderr, []byte(`"retry_delay_ms":`)) {
				t.Fatal("retry attempt diagnostics missing")
			}
		}
		for _, secret := range []string{server.URL, "fp_retries-private", "body-private", "cookie-private"} {
			if bytes.Contains(result.stderr, []byte(secret)) {
				t.Fatal("retry diagnostics leaked request or response content")
			}
		}
	}
	before := requests.Load()
	invalid := tg.exec(t, "fp_retries-private", tg.home, time.Minute, "--http-retries=6", "billing", "usage", "summary")
	if invalid.exit == 0 || requests.Load() != before {
		t.Fatal("invalid retry count contacted API")
	}
	failures.Store(1)
	result := tg.exec(t, "fp_retries-private", tg.home, time.Minute, "billing", "usage", "summary")
	if result.exit == 0 || requests.Load()-before != 1 || len(result.stdout) != 0 {
		t.Fatal("read retries persisted into a later invocation")
	}
}
