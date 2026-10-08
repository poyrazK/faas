package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #4 (H4-29): `traffic status` took only a positional slug
// while `traffic set|promote` took only --app, so `traffic promote <slug>
// --deployment v7` failed with "unexpected positional argument(s)". Every
// traffic leaf now accepts either spelling.
func TestTrafficLeavesAcceptSlugEitherWay(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/deployments" {
			writeJSONTest(w, api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "0123456789abcdef0123456789abcdef", Revision: 7, Status: statusLive, TrafficPercent: 50},
			}})
			return
		}
		writeJSONTest(w, api.DeploymentResponse{ID: "0123456789abcdef0123456789abcdef", Revision: 7, Status: statusLive, TrafficPercent: 50})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	_, restore := captureStdout(t)
	statusCode := cmdTraffic([]string{"status", "--app", "demo"})
	setCode := cmdTraffic([]string{"set", "demo", "--deployment", "v7", "--percent", "50"})
	restore()
	if statusCode != 0 || setCode != 0 {
		t.Fatalf("status --app exit=%d, set <slug> exit=%d, want 0 and 0", statusCode, setCode)
	}
	mu.Lock()
	got := strings.Join(seen, "\n")
	mu.Unlock()
	if strings.Count(got, "GET /v1/apps/demo/deployments") < 2 {
		t.Fatalf("requests = %q, want status and the v7 lookup to read demo's deployments", got)
	}

	stderr, restoreErr := captureStderr(t)
	code := cmdTraffic([]string{"promote", "web", "--app", "demo", "--deployment", "v7"})
	restoreErr()
	if code == 0 || !strings.Contains(stderr.String(), "name different apps") {
		t.Fatalf("promote with conflicting slugs exit=%d stderr=%q, want a conflict error", code, stderr.String())
	}
	if code := cmdTraffic([]string{"promote", "demo", "--deployment", "v7", "extra"}); code == 0 {
		t.Fatal("a trailing positional after the flags must still be rejected")
	}
	// hunt #8: the generated usage prints the slug after the flags.
	_, restore = captureStdout(t)
	trailing := cmdTraffic([]string{"set", "--deployment", "v7", "--percent", "50", "demo"})
	restore()
	if trailing != 0 {
		t.Fatalf("set --deployment v7 --percent 50 demo exit=%d, want 0 (the documented order)", trailing)
	}
	if code := cmdTraffic([]string{"set", "--deployment", "v7", "--percent", "50", "demo", "extra"}); code == 0 {
		t.Fatal("two trailing positionals must be rejected")
	}
}
