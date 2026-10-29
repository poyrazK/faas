package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: every `gregale queue` leaf (status, state, peek,
// dead-letter, send, receive, ack, tail) rejected --app with "unknown flag"
// although `gregale link` tells users to pass it (Hunt 14 H14-57).
func TestQueueLeavesAcceptTheAppFlag(t *testing.T) {
	var stateCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/h14-q/queues/state" {
			stateCalls.Add(1)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	main.ResetJSONOut(t)

	stderr, restore := main.CaptureStderr(t)
	code := main.CmdQueueState([]string{"--app", "h14-q"})
	restore()
	if code != 0 || stateCalls.Load() != 1 || strings.Contains(stderr.String(), "unknown flag") {
		t.Fatalf("queue state --app: exit %d, calls %d: %s", code, stateCalls.Load(), stderr.String())
	}
	stderr, restore = main.CaptureStderr(t)
	code = main.CmdQueueState([]string{"h14-q", "--app", "other"})
	restore()
	if code == 0 || !strings.Contains(stderr.String(), "pass the app once") {
		t.Fatalf("conflicting slug and --app: exit %d: %s", code, stderr.String())
	}
}
