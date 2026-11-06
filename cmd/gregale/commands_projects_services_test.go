package main_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// projectServicesAPI serves a two-service project where web calls api, and
// records the writes the CLI sends.
func projectServicesAPI(t *testing.T) *[]string {
	t.Helper()
	var mu sync.Mutex
	calls := &[]string{}
	workloads := `[{"slug":"shop-web","workload_name":"web","status":"active","depends_on":["api"]},
		{"slug":"shop-api","workload_name":"api","status":"active"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		record := func() {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			*calls = append(*calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+string(body))
			mu.Unlock()
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/shop":
			_, _ = w.Write([]byte(`{"slug":"shop","workloads":` + workloads + `,"exclusions":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/shop/delete-preview":
			_, _ = w.Write([]byte(`{"project":{"slug":"shop"},"workloads":` + workloads + `,"domain_count":0,"env_count":0,"cron_count":0}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/projects/shop":
			record()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/v1/apps/") && strings.Contains(r.URL.Path, "/secrets/"):
			record()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	return calls
}

// Deleting a project detaches its services, and their calls to each other by
// service name stop resolving at once (ADR-20261104221217).
func TestProjectDeleteRefusesWhileServicesCallEachOther(t *testing.T) {
	calls := projectServicesAPI(t)
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"projects", "rm", "shop", "--dry-run"})
	restore()
	if code != 0 || !strings.Contains(out.String(), "these calls stop working: web -> api") {
		t.Fatalf("dry run exit %d:\n%s", code, out)
	}
	if code := main.Run([]string{"projects", "rm", "shop", "--yes"}); code == 0 || len(*calls) != 0 {
		t.Fatalf("--yes exit %d, writes %v; want a refusal and no delete", code, *calls)
	}
	_, restore = main.CaptureStdout(t)
	code = main.Run([]string{"projects", "rm", "shop", "--yes", "--detach-services"})
	restore()
	if code != 0 || len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "DELETE /v1/projects/shop") {
		t.Fatalf("--detach-services exit %d, writes %v", code, *calls)
	}
}

// A pull request preview value is set once for every service of the project
// (ADR-20261104221217).
func TestProjectPreviewSecretsSetEveryService(t *testing.T) {
	calls := projectServicesAPI(t)
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"projects", "preview-secrets", "set", "shop", "STRIPE_KEY=sk_test_1"})
	restore()
	if code != 0 || len(*calls) != 2 {
		t.Fatalf("exit %d, writes %v:\n%s", code, *calls, out)
	}
	for i, slug := range []string{"shop-web", "shop-api"} {
		call := (*calls)[i]
		if !strings.HasPrefix(call, "PUT /v1/apps/"+slug+"/secrets/STRIPE_KEY?") || !strings.Contains(call, "pr-preview") || !strings.Contains(call, "sk_test_1") {
			t.Fatalf("write %d = %s, want STRIPE_KEY in %s's pr-preview scope", i, call, slug)
		}
	}
	*calls = nil
	_, restore = main.CaptureStdout(t)
	code = main.Run([]string{"projects", "preview-secrets", "unset", "shop", "STRIPE_KEY"})
	restore()
	if code != 0 || len(*calls) != 2 || !strings.HasPrefix((*calls)[0], "DELETE /v1/apps/shop-web/secrets/STRIPE_KEY?") {
		t.Fatalf("unset exit %d, writes %v", code, *calls)
	}
	if code := main.Run([]string{"projects", "preview-secrets", "set", "shop", "not a pair"}); code == 0 {
		t.Fatal("an invalid pair was accepted")
	}
}
