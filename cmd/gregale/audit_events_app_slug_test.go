package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestAuditEventsListTakesAnAppSlug: `audit-events list --app h14-py`
// answered "unknown flag --app" on production-us rc.294; only --app-id
// <uuid> worked (Hunt 14 H14-64).
func TestAuditEventsListTakesAnAppSlug(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/h14-py" {
			_, _ = w.Write([]byte(`{"id":"ff463f83-b8a5-4566-96b5-83ea5de6fe4a","slug":"h14-py"}`))
			return
		}
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"events":[]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"audit-events", "list", "--app", "h14-py"})
	restore()
	if code != 0 || !strings.Contains(query, "ff463f83-b8a5-4566-96b5-83ea5de6fe4a") {
		t.Fatalf("exit=%d query=%q stderr=%q", code, query, stderr())
	}
	_, stderr, restore = main.SwapIO(t)
	code = main.Run([]string{"audit-events", "list", "--app", "h14-py", "--app-id", "ff463f83-b8a5-4566-96b5-83ea5de6fe4a"})
	restore()
	if code != 1 || !strings.Contains(stderr(), "--app <slug> | --app-id <uuid>") {
		t.Fatalf("both selectors: exit=%d stderr=%q", code, stderr())
	}
}

// TestInvocationsListExplainsItHasNoAppFilter: `invocations list --app
// h14-py` answered only "unknown flag --app" on production-us rc.294 (Hunt
// 14 H14-64); the ledger is account-wide.
func TestInvocationsListExplainsItHasNoAppFilter(t *testing.T) {
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"invocations", "list", "--app", "h14-py"})
	restore()
	if code != 1 || !strings.Contains(stderr(), "it has no --app filter") || !strings.Contains(stderr(), "gregale debug requests list <slug>") {
		t.Fatalf("exit=%d stderr=%q", code, stderr())
	}
}
