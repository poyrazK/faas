package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestDLQGetTakesTheEventIDWithAppFlag pins Hunt 32 H32-22: `dlq get
// <event-id> --app h32-qw` read the event id as a second app and failed with
// "pass the app once".
func TestDLQGetTakesTheEventIDWithAppFlag(t *testing.T) {
	const event = "eca7e025-362c-4c47-973e-74bc5494fcb2"
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + event + `","source":"invocation","error_detail":{}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, verb := range []string{"get", "inspect"} {
		_, errOut, restore := main.SwapIO(t)
		code := main.Run([]string{"dlq", verb, event, "--app", "h32-qw"})
		stderr := errOut()
		restore()
		if code != 0 {
			t.Fatalf("dlq %s exit = %d: %s", verb, code, stderr)
		}
		if !strings.Contains(path, "/apps/h32-qw/") || !strings.HasSuffix(path, event) {
			t.Fatalf("dlq %s requested %s", verb, path)
		}
	}
}
