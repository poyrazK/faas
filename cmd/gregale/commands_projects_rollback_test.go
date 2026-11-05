package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// projectRollbackAPI serves a three-service project (web → api → billing) and
// records the order of per-app rollbacks; failSlug answers 409.
func projectRollbackAPI(t *testing.T, failSlug string) *[]string {
	t.Helper()
	var mu sync.Mutex
	calls := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/shop":
			_, _ = w.Write([]byte(`{"slug":"shop","workloads":[
				{"slug":"shop-web","workload_name":"web","status":"active","depends_on":["api"]},
				{"slug":"shop-api","workload_name":"api","status":"active","depends_on":["billing"]},
				{"slug":"shop-billing","workload_name":"billing","status":"active"}],"exclusions":[]}`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/apps/") && strings.HasSuffix(r.URL.Path, "/rollback"):
			slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/rollback")
			mu.Lock()
			*calls = append(*calls, slug)
			mu.Unlock()
			if slug == failSlug {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"no_previous_deployment","detail":"no previous deployment"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"dep-` + slug + `","status":"live"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	return calls
}

func TestProjectRollbackRollsServicesBackInDependencyOrder(t *testing.T) {
	calls := projectRollbackAPI(t, "")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"projects", "rollback", "shop", "--yes"})
	restore()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := strings.Join(*calls, ","); got != "shop-billing,shop-api,shop-web" {
		t.Fatalf("rollback order = %s, want billing before api before web", got)
	}
	if !strings.Contains(out.String(), "Project shop rolled back: 3 services") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestProjectRollbackDryRunChangesNothing(t *testing.T) {
	calls := projectRollbackAPI(t, "")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"projects", "rollback", "shop", "--dry-run"})
	restore()
	if code != 0 || len(*calls) != 0 {
		t.Fatalf("exit %d, rollbacks %v:\n%s", code, *calls, out)
	}
	got := out.String()
	if !strings.Contains(got, "1. billing (shop-billing)") || !strings.Contains(got, "3. web (shop-web) (after api)") {
		t.Fatalf("plan:\n%s", got)
	}
}

func TestProjectRollbackStopsAtTheFirstFailure(t *testing.T) {
	calls := projectRollbackAPI(t, "shop-api")
	_, restore := main.CaptureStdout(t)
	code := main.Run([]string{"projects", "rollback", "shop", "--yes"})
	restore()
	if code == 0 {
		t.Fatal("exit 0 after a failed service rollback")
	}
	if got := strings.Join(*calls, ","); got != "shop-billing,shop-api" {
		t.Fatalf("rollbacks = %s, want web left alone after api failed", got)
	}
}

func TestProjectRollbackRequiresDryRunOrYes(t *testing.T) {
	calls := projectRollbackAPI(t, "")
	if code := main.Run([]string{"projects", "rollback", "shop"}); code == 0 || len(*calls) != 0 {
		t.Fatalf("exit %d, rollbacks %v; want a usage error and no change", code, *calls)
	}
}
