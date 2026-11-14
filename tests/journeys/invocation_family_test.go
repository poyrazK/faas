package journeys

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvocationFamilyIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	var posts, reads atomic.Int32
	const invocation = `{"id":"inv-family","state":"completed","result":{"ok":true}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/apps/billing/invoke/async":
			posts.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"inv-family","status_url":"/v1/invocations/inv-family"}`))
		case "GET /v1/invocations/inv-family":
			reads.Add(1)
			_, _ = w.Write([]byte(invocation))
		case "GET /v1/invocations":
			reads.Add(1)
			_, _ = w.Write([]byte(`{"invocations":[` + invocation + `]}`))
		default:
			t.Errorf("family changed API routing: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	for _, args := range [][]string{
		{"invocation", "run", "billing", "--wait", "--timeout", "2s", "--interval", "5ms"},
		{"invocation", "list"},
		{"invocation", "get", "inv-family"},
		{"invocation", "wait", "inv-family", "--timeout", "2s"},
	} {
		res := tg.exec(t, "fp_invocation-family-private", tg.home, time.Minute, append([]string{"--json"}, args...)...)
		if res.exit != 0 || !bytes.Contains(res.stdout, []byte("inv-family")) || !bytes.Contains(res.stdout, []byte("completed")) {
			t.Fatalf("family command: exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
		}
	}
	if posts.Load() != 1 || reads.Load() != 4 {
		t.Fatalf("unexpected requests: posts=%d reads=%d", posts.Load(), reads.Load())
	}
}
