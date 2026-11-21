package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskExecutionCLIInterruptedClaimNeverRepeats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private Task execution profiles require POSIX file permissions")
	}
	root, plan := taskReviewFixture(t)
	modules := filepath.Join(root, "worker", "node_modules")
	if err := os.Mkdir(modules, 0700); err != nil {
		t.Fatal(err)
	}
	dependency := filepath.Join(modules, "fixture.js")
	if err := os.WriteFile(dependency, []byte("owner-installed"), 0600); err != nil {
		t.Fatal(err)
	}
	account, key, owner := uuid.NewString(), uuid.NewString(), uuid.NewString()
	id, operation := strings.Repeat("8", 64), strings.Repeat("9", 64)
	reviews := map[string]api.AppReleaseReview{}
	for _, role := range []string{"web", "candidate", "observer"} {
		reviews[role] = api.AppReleaseReview{AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("a", 64), ServingDeploymentIDs: []string{}}
	}
	var saved api.TaskToolReleaseIntentRecord
	claims := 0
	failCheckpoint := false
	checkpointAttempts := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out any
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/apps/"):
			out = reviews[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/release-review")]
		case r.Method == "GET":
			out = saved
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/decision"):
			var in api.TaskToolReleaseIntentDecision
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				w.WriteHeader(400)
				return
			}
			var err error
			if in.Decision == "authorize" {
				in.AuthorizerKeyID = owner
				saved, err = tasktoolrelease.Execution(saved, in, true, time.Now())
			} else {
				saved, err = tasktoolrelease.Decide(saved, in, time.Now())
			}
			if err != nil {
				t.Error(err)
				w.WriteHeader(409)
				return
			}
			out = saved
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/check"):
			var in api.TaskToolReleaseIntentCheck
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Execution == nil {
				w.WriteHeader(400)
				return
			}
			if in.Execution.Receipt != nil && failCheckpoint {
				checkpointAttempts++
				w.WriteHeader(503)
				return
			}
			if in.Execution.Action == "claim" {
				claims++
			}
			var err error
			saved, err = tasktoolrelease.Execution(saved, api.TaskToolReleaseIntentDecision{ReleaseFingerprint: in.ReleaseFingerprint, Execution: in.Execution}, true, time.Now())
			if err != nil {
				t.Error(err)
				w.WriteHeader(409)
				return
			}
			out = saved
		case r.Method == "PUT":
			var in api.TaskToolReleaseIntent
			if json.NewDecoder(r.Body).Decode(&in) != nil {
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
		default:
			t.Error("unexpected native action", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer backend.Close()
	configDir := t.TempDir()
	t.Setenv("HOME", configDir)
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("FAAS_API", backend.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_fixture")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Cleanup(main.ResetCapabilityView)
	t.Setenv("MCP_TASK_NAMESPACE", "approved-queue")
	t.Setenv("PATH", t.TempDir()) // Real Node must never run in this interrupted fixture.
	bindings := filepath.Join(t.TempDir(), "bindings.json")
	env := map[string]string{"MCP_TASK_NAMESPACE": "approved-queue", "DATABASE_URL": "postgres://runtime/SECRET_SENTINEL", "MCP_TASK_MIGRATION_DATABASE_URL": "postgres://migration/SECRET_SENTINEL", "MCP_TASK_OBSERVER_DATABASE_URL": "postgres://observer/SECRET_SENTINEL", "MCP_TASK_OWNER_KEY": strings.Repeat("k", 32), "MCP_UNRESOLVED_TOKEN": "approved-fixture-token"}
	body, _ := json.Marshal(env)
	if err := os.WriteFile(bindings, body, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(want int, args ...string) string {
		t.Helper()
		code, output, stderr := runCLI(t, append([]string{"--json", "mcp", "server", "tasks"}, args...)...)
		if code != want {
			t.Fatalf("code %d want %d: %s %s", code, want, output, stderr)
		}
		if strings.Contains(output+stderr, "SECRET_SENTINEL") || strings.Contains(output, bindings) {
			t.Fatal("private binding leaked")
		}
		return output
	}
	run(0, "intent", "prepare", "--id", id, "--plan", plan, "--reason", "Review queue release")
	run(0, "intent", "approve", "--id", id, "--plan", plan, "--intent-sha256", saved.IntentSHA256)
	run(0, "execution", "authorize", "--id", id, "--operation-id", operation, "--plan", plan, "--bindings-file", bindings, "--intent-sha256", saved.IntentSHA256, "--allow-tool-execution")
	if saved.Execution == nil || saved.Execution.State != "authorized" {
		t.Fatal("no separate authority", saved)
	}
	configBase, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := filepath.Glob(filepath.Join(configBase, "gregale", "task-executions", "*", id, "profile.json"))
	if err != nil || len(profiles) != 1 {
		t.Fatal("private profile missing", err)
	}
	original, err := os.ReadFile(profiles[0])
	if err != nil {
		t.Fatal(err)
	}
	var changed map[string]any
	if json.Unmarshal(original, &changed) != nil {
		t.Fatal("profile JSON invalid")
	}
	changed["env"].(map[string]any)["DATABASE_URL"] = "postgres://different/SECRET_SENTINEL"
	modified, _ := json.Marshal(changed)
	if err := os.WriteFile(profiles[0], modified, 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "execution", "apply", "--id", id, "--operation-id", operation, "--allow-tool-execution")
	if claims != 0 {
		t.Fatal("changed binding was claimed")
	}
	if err := os.WriteFile(profiles[0], original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dependency, []byte("unreviewed replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "execution", "apply", "--id", id, "--operation-id", operation, "--allow-tool-execution")
	if claims != 0 {
		t.Fatal("changed local dependencies were claimed")
	}
	if err := os.WriteFile(dependency, []byte("owner-installed"), 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "execution", "apply", "--id", id, "--operation-id", operation, "--allow-tool-execution")
	if saved.Execution.State != "reconcile_required" || claims != 1 {
		t.Fatal("failed preflight did not retain its one claim", saved, claims)
	}
	if err := os.Rename(plan, plan+".missing"); err != nil {
		t.Fatal(err)
	}
	run(0, "execution", "get", "--id", id)
	for _, action := range []string{"inspect", "plan-recovery"} {
		output := run(0, "execution", action, "--id", id)
		var report tasktoolrelease.RecoveryReport
		if json.Unmarshal([]byte(output), &report) != nil || !report.ReadOnly || report.ReadyToApply || !report.OwnerAuthorizationRequired {
			t.Fatal("missing local files changed recovery authority", output)
		}
	}
	run(0, "execution", "apply", "--id", id, "--operation-id", operation, "--allow-tool-execution")
	if claims != 1 {
		t.Fatal("interrupted execution was repeated", claims)
	}
	// Acknowledgement of the first native checkpoint must precede the gate,
	// even after local fsync and read-only binding verification succeeded.
	if err := os.Rename(plan+".missing", plan); err != nil {
		t.Fatal(err)
	}
	id, operation = strings.Repeat("a", 64), strings.Repeat("b", 64)
	claims = 0
	failCheckpoint = true
	marker := filepath.Join(t.TempDir(), "gate-must-not-run")
	env["MCP_UNRESOLVED_TOKEN"] = marker
	body, _ = json.Marshal(env)
	if err := os.WriteFile(bindings, body, 0600); err != nil {
		t.Fatal(err)
	}
	nodeDir := t.TempDir()
	node := `#!/bin/sh
case "$1" in
  --input-type=module) printf '{"verified":true}' ;;
  *) printf 'unexpected gate' > "$MCP_UNRESOLVED_TOKEN"; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(nodeDir, "node"), []byte(node), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", nodeDir)
	run(0, "intent", "prepare", "--id", id, "--plan", plan, "--reason", "Review checkpoint acknowledgement")
	run(0, "intent", "approve", "--id", id, "--plan", plan, "--intent-sha256", saved.IntentSHA256)
	run(0, "execution", "authorize", "--id", id, "--operation-id", operation, "--plan", plan, "--bindings-file", bindings, "--intent-sha256", saved.IntentSHA256, "--allow-tool-execution")
	// HTTP 503 uses the CLI temporary-failure exit code, while retaining the claim.
	run(3, "execution", "apply", "--id", id, "--operation-id", operation, "--allow-tool-execution")
	if checkpointAttempts < 1 || claims != 1 || saved.Execution.State != "reconcile_required" {
		t.Fatal("missing acknowledgement did not retain its original claim", checkpointAttempts, claims, saved.Execution.State)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("gate ran before native checkpoint acknowledgement", err)
	}
}
