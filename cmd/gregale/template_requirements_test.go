// adr: 20261023115741 — templates a default account cannot deploy (#5756).

package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// accountHidingPreviews answers GET /v1/capabilities for an account without
// queue bindings or event subscriptions, the default on production, and
// records every other request.
func accountHidingPreviews(t *testing.T) (*[]string, *sync.Mutex) {
	t.Helper()
	clearCapabilityReleaseAll(t)
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{Plan: "scale", HiddenCapabilities: []string{"queue-bindings", "event-subscriptions"}})
			return
		}
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test: unhandled"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	return &calls, &mu
}

// `gregale init --list` offered queue-worker and event-worker to an account
// that could deploy neither (a gated 404 and capability_not_released), and
// gave no sign that cron-worker needs the customer's own Upstash.
func TestInitListLeavesOutTemplatesTheAccountCannotDeploy(t *testing.T) {
	accountHidingPreviews(t)
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	defer func() { *main.OsStdout = old }()
	if code := main.Run([]string{"init", "--list"}); code != 0 {
		t.Fatalf("init --list exit = %d", code)
	}
	out := stdout.String()
	for _, hidden := range []string{"queue-worker", "event-worker"} {
		if strings.Contains(out, hidden) {
			t.Errorf("init --list offers %s, which needs a capability the account does not have:\n%s", hidden, out)
		}
	}
	if !strings.Contains(out, "\n  hello-node ") {
		t.Errorf("init --list lost an ordinary template:\n%s", out)
	}
	if !strings.Contains(out, "cron-worker") || !strings.Contains(out, "needs QSTASH_CURRENT_SIGNING_KEY") {
		t.Errorf("init --list does not say what cron-worker needs:\n%s", out)
	}
}

// Deploying queue-worker created the app and the release, then failed on
// the gated queue-binding route with a bare "no such API route". It is now
// refused before anything is created, naming the capability.
func TestDeployRefusesTemplateNeedingAHiddenCapability(t *testing.T) {
	calls, mu := accountHidingPreviews(t)
	stderr, restore := main.CaptureStderr(t)
	code := main.Run([]string{"deploy", "--template", "queue-worker", "--name", "qw-app", "--yes"})
	restore()
	if code == 0 {
		t.Fatal("deploy --template queue-worker succeeded for an account without queue bindings")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, call := range *calls {
		if strings.HasPrefix(call, "POST ") || strings.HasPrefix(call, "PUT ") || strings.HasPrefix(call, "PATCH ") {
			t.Errorf("refused deploy still sent %s", call)
		}
	}
	if msg := stderr.String(); !strings.Contains(msg, "Queue bindings") || !strings.Contains(msg, "feature list --early-access") {
		t.Errorf("refusal does not name the capability and the way to request it:\n%s", msg)
	}
}
