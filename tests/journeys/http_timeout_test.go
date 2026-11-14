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

// GA row 156: a read-only request with an invocation-scoped transport budget.
func TestHTTPTimeoutLiveAPI(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	r := tg.ok(t, 2*time.Minute, "--http-timeout", "90s", "billing", "usage", "summary")
	objects(t, r.stdout)
}

func TestHTTPTimeoutIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	var requests atomic.Int32
	var slow atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/usage/summary" || r.Header.Get("Authorization") != "Bearer fp_timeout-private" {
			t.Error("timeout changed the API target or authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if slow.Load() {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	plain := tg.exec(t, "fp_timeout-private", tg.home, time.Minute, "billing", "usage", "summary")
	explicit := tg.exec(t, "fp_timeout-private", tg.home, time.Minute, "--http-timeout=90s", "billing", "usage", "summary")
	if plain.exit != 0 || explicit.exit != 0 || !bytes.Equal(plain.stdout, explicit.stdout) {
		t.Fatal("explicit timeout changed successful JSON output")
	}
	before := requests.Load()
	invalid := tg.exec(t, "fp_timeout-private", tg.home, time.Minute, "--http-timeout=invalid", "billing", "usage", "summary")
	if invalid.exit == 0 || requests.Load() != before {
		t.Fatal("invalid timeout contacted the API")
	}
	slow.Store(true)
	expired := tg.exec(t, "fp_timeout-private", tg.home, time.Minute, "--http-timeout=100ms", "billing", "usage", "summary")
	if expired.exit == 0 || len(expired.stdout) != 0 || requests.Load() != before+1 {
		t.Fatalf("body deadline did not fail cleanly: exit=%d stderr=%s", expired.exit, expired.stderr)
	}
	if bytes.Contains(expired.stderr, []byte("fp_timeout-private")) {
		t.Fatal("timeout error exposed authentication")
	}
	// The override is neither persisted nor inherited by the next invocation.
	after := tg.exec(t, "fp_timeout-private", tg.home, time.Minute, "billing", "usage", "summary")
	if after.exit != 0 || !bytes.Equal(after.stdout, plain.stdout) {
		t.Fatal("request timeout persisted into a later invocation")
	}
}
