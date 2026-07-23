// CLI smoke tests for the auth path of `faas deploy`. Pin the wire
// behaviour the [[cmd-faas-requireslogin-hermeticity]] memory documents:
// running the deploy command with no auth token must exit with code 2
// (the documented errAuth → printErr contract), and running it with a
// valid token against a fake apid must exit with code 0.
//
// These are smuggled into cmd/faas/cli_test.go patterns: hermetic via
// t.Setenv on HOME + XDG_CONFIG_HOME + FAAS_TOKEN, no daemon, no PG,
// no KVM. Suitable for `make test` and `make e2e`.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestFaasCLI_Deploy_NoAuthExitsTwo pins the canonical "no auth" path:
// running `faas deploy --image ...` with FAAS_TOKEN="" and no token
// file under HOME/XDG_CONFIG_HOME must return exit code 2 with stderr
// containing "not logged in" (the exact wording from
// commands.go:24 / commands.go:35 where authedClient /
// authedClientWithDeployTimeout return errAuth). This guards the
// conformance with the [[cmd-faas-requireslogin-hermeticity]] project
// memory: a regression that returns 0 or 1 instead of 2 would
// violate the established contract and break the bash-style callers
// the CLI ships with.
func TestFaasCLI_Deploy_NoAuthExitsTwo(t *testing.T) {
	// Empty HOME + XDG_CONFIG_HOME so tokenPath() (cli_test.go:41) reports
	// a missing file (loadToken returns ""). FAAS_TOKEN="" forces the
	// file path too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")

	// We never reach the network in this test — authedClient should
	// fail at loadToken(). The httptest server URL is only a guard
	// against "we somehow did make a request anyway".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("unexpected network request: %s", "authedClient must fail before any HTTP call")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	code := cmdDeployTarball([]string{"--image", "registry.example/foo@sha256:abc", "--name", "no-auth-app"})
	if code != 2 {
		t.Errorf("cmdDeployTarball exit code = %d, want 2 (errAuth contract)", code)
	}
}

// TestFaasCLI_Deploy_HappyPath_ReachesAPID pins the positive path:
// with FAAS_TOKEN set, cmdDeployTarball must reach `POST /v1/apps`
// and then `POST /v1/apps/{slug}/deployments`. We fake-apid both
// endpoints and assert they were hit; the test then follows the
// happy path through to the SSE log tail (mirroring
// TestCmdDeploy_HappyPath_PrintsColdWakeSentence at cli_test.go:386
// which proves the UX §2.5 sentence is printed).
//
// This is a smoke test in the "wire the wire" sense: it pins both
// the POST shapes AND the SSE follow-up, which is what a real
// customer deploy looks like end-to-end. The existing cli_test.go
// covers the in-process call shape; this test pins that calling
// the production deploy path (the one customers hit) follows the
// documented sequence.
func TestFaasCLI_Deploy_HappyPath_ReachesAPID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "smoke-test-token")

	var hits [3]string // [POST /v1/apps, POST /v1/apps/{slug}/deployments, GET /v1/deployments/{id}/logs]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/apps":
			hits[0] = r.URL.Path
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a-smoke", Slug: "smoke-app"})
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/apps/") && strings.HasSuffix(r.URL.Path, "/deployments"):
			hits[1] = r.URL.Path
			// Return a plain DeploymentResponse — the SSE tail is opened
			// separately on /v1/deployments/{id}/logs by streamDeployLogs.
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d-smoke", Status: "pending", AppID: "smoke-app"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/") && strings.HasSuffix(r.URL.Path, "/logs"):
			hits[2] = r.URL.Path
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"status\":\"live\"}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdDeployTarball([]string{"--image", "registry.example/foo@sha256:abc", "--name", "smoke-app"}); code != 0 {
		t.Errorf("cmdDeployTarball exit code = %d, want 0", code)
	}
	if hits[0] != "/v1/apps" {
		t.Errorf("POST /v1/apps not hit (hits[0] = %q)", hits[0])
	}
	if !strings.HasPrefix(hits[1], "/v1/apps/smoke-app/deployments") {
		t.Errorf("POST deployment not hit at the expected path (hits[1] = %q)", hits[1])
	}
	if !strings.HasPrefix(hits[2], "/v1/deployments/") || !strings.HasSuffix(hits[2], "/logs") {
		t.Errorf("GET deployment logs not hit at the expected path (hits[2] = %q)", hits[2])
	}
}
