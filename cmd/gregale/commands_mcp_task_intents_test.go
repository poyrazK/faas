package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskIntentCLIReviewJourney(t *testing.T) {
	root, plan := taskReviewFixture(t)
	account, key, id := uuid.NewString(), uuid.NewString(), strings.Repeat("d", 64)
	reviews := map[string]api.AppReleaseReview{}
	for _, role := range []string{"web", "candidate", "observer"} {
		reviews[role] = api.AppReleaseReview{AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("e", 64), ServingDeploymentIDs: []string{}}
	}
	var saved api.TaskToolReleaseIntentRecord
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("fresh review was routed through cached idempotency results")
		}
		var out any
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/apps/") && strings.HasSuffix(r.URL.Path, "/release-review"):
			out = reviews[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/release-review")]
		case r.Method == "PUT" && r.URL.Path == "/v1/task-tool-release-intents/"+id:
			var in api.TaskToolReleaseIntent
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var err error
			saved, err = tasktoolrelease.New(account, key, id, in, time.Now())
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			out = saved
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/decision"):
			var in api.TaskToolReleaseIntentDecision
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var err error
			saved, err = tasktoolrelease.Decide(saved, in, time.Now())
			if err != nil {
				t.Error(err)
				w.WriteHeader(409)
				return
			}
			out = saved
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/check"):
			var in api.TaskToolReleaseIntentCheck
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			saved = tasktoolrelease.Check(saved, in.ReleaseFingerprint, true, time.Now())
			out = saved
		default:
			t.Error("unexpected Task review request", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer backend.Close()
	t.Setenv("FAAS_API", backend.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_fake_fixture")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("PATH", t.TempDir())
	run := func(action, want string, extra ...string) {
		t.Helper()
		args := append([]string{"mcp", "server", "tasks", "intent", action, "--plan", plan, "--id", id, "--json"}, extra...)
		code, output, stderr := runCLI(t, args...)
		var out api.TaskToolReleaseIntentRecord
		if code != 0 || json.Unmarshal([]byte(output), &out) != nil || out.Status != want || out.ReadyToApply || out.BindingsVerified {
			t.Fatalf("%s: %d %s %s", action, code, output, stderr)
		}
		if strings.Contains(output, root) || strings.Contains(output, "MCP_TASK_OWNER_KEY") {
			t.Fatal("intent exposed a path or credential reference")
		}
	}
	run("prepare", "pending", "--reason", "Review reports")
	if requests != 4 {
		t.Fatal("prepare did more than inspect three apps and save metadata", requests)
	}
	run("approve", "approved", "--intent-sha256", saved.IntentSHA256)
	run("get", "approved")
	if err := os.WriteFile(filepath.Join(root, "worker", "server.js"), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	run("get", "stale")
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 3 {
		t.Fatal("review created local release artifacts", err)
	}
}
