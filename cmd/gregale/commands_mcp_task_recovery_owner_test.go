package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTaskRecoveryOwnerCLIStopsWithoutLocalFiles(t *testing.T) {
	r := api.TaskToolReleaseIntentRecord{Version: 1, ID: strings.Repeat("1", 64), AccountID: uuid.NewString(), RequesterKeyID: uuid.NewString(), IntentSHA256: strings.Repeat("2", 64), Status: "expired", Intent: api.TaskToolReleaseIntent{ReleaseFingerprint: strings.Repeat("3", 64)},
		Execution: &api.TaskToolReleaseExecution{OperationID: strings.Repeat("4", 64), BindingsSHA256: strings.Repeat("5", 64), State: "reconcile_required", FenceVersion: 1, Receipt: &api.TaskToolReleaseReceipt{Revision: 8}}}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "PUT" {
			var in api.TaskToolReleaseIntentDecision
			if json.NewDecoder(req.Body).Decode(&in) != nil || in.Decision != "recovery_stop" || in.Execution != nil || in.Recovery == nil || in.Recovery.Revision != 8 {
				t.Error("invalid stop request")
				w.WriteHeader(400)
				return
			}
			writes++
			r.Execution.Recovery = &api.TaskToolRecoveryHandover{ID: in.Recovery.HandoverID, OperationID: r.Execution.OperationID, Revision: 8, State: "stopping", RequestedAt: time.Now()}
		}
		_ = json.NewEncoder(w).Encode(r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "owner-fixture")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Cleanup(main.ResetCapabilityView)
	handover := strings.Repeat("6", 64)
	args := []string{"--json", "mcp", "server", "tasks", "execution", "recovery-stop", "--id", r.ID, "--operation-id", r.Execution.OperationID, "--handover-id", handover, "--revision", "8"}
	code, out, stderr := runCLI(t, args...)
	if code != 0 || writes != 1 || !strings.Contains(out, "stopping") || stderr != "" {
		t.Fatalf("stop: %d %s %s", code, out, stderr)
	}
	args[5] = "recovery-qualify"
	args = append(args, "--allow-local-checks")
	code, _, _ = runCLI(t, args...)
	if code == 0 || writes != 1 {
		t.Fatal("unsettled handover ran qualification")
	}
	args[5] = "recovery-apply"
	code, _, _ = runCLI(t, args...)
	if code == 0 || writes != 1 {
		t.Fatal("recovery apply exposed")
	}
	if _, err := os.UserConfigDir(); err != nil {
		t.Fatal(err)
	}
}

func TestMCPTaskExecutionFenceInheritedOnlyOnNativeWrites(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fenced := r.Header.Get(api.TaskExecutionClaimHeader) != ""
		expected := r.Method != "GET" && !strings.HasPrefix(r.URL.Path, "/v1/task-tool-release-intents/")
		if fenced != expected {
			t.Errorf("fence on %s %s: %v", r.Method, r.URL.Path, fenced)
		}
		if fenced && (r.Header.Get(api.TaskExecutionIntentHeader) != strings.Repeat("1", 64) || r.Header.Get(api.TaskExecutionOperationHeader) != strings.Repeat("2", 64) || r.Header.Get(api.TaskExecutionClaimHeader) != strings.Repeat("3", 64)) {
			t.Error("fence identity changed")
		}
		if strings.HasSuffix(r.URL.Path, "/traffic") {
			_, _ = w.Write([]byte(`{}`))
		} else if r.Method == "POST" {
			_, _ = w.Write([]byte(`{}`))
		} else {
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	t.Setenv("GREGALE_TASK_EXECUTION_FENCE", strings.Repeat("1", 64)+":"+strings.Repeat("2", 64)+":"+strings.Repeat("3", 64))
	t.Setenv("GREGALE_TASK_EXECUTION_API", server.URL)
	c := main.NewClient(server.URL, "fixture")
	_, _ = c.GetTaskToolReleaseIntent(t.Context(), strings.Repeat("1", 64))
	_, _ = c.CheckTaskToolReleaseIntent(t.Context(), strings.Repeat("1", 64), api.TaskToolReleaseIntentCheck{})
	_, _ = c.PatchDeploymentTrafficIfServingWithMCPAccess(t.Context(), uuid.NewString(), 100, uuid.NewString(), nil)
	if requests != 3 {
		t.Fatal("missing test request", requests)
	}
}

func TestMCPTaskExecutionFenceRefusesForeignRedirect(t *testing.T) {
	reached := false
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(200) }))
	defer foreign.Close()
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer native.Close()
	t.Setenv("GREGALE_TASK_EXECUTION_API", native.URL)
	t.Setenv("GREGALE_TASK_EXECUTION_FENCE", strings.Repeat("1", 64)+":"+strings.Repeat("2", 64)+":"+strings.Repeat("3", 64))
	c := main.NewClient(native.URL, "fixture")
	if _, err := c.PatchDeploymentTrafficIfServingWithMCPAccess(t.Context(), uuid.NewString(), 100, uuid.NewString(), nil); err == nil || reached {
		t.Fatal("private executor fence left its native origin", err)
	}
	c = main.NewClient(foreign.URL, "fixture")
	if _, err := c.PatchDeploymentTrafficIfServingWithMCPAccess(t.Context(), uuid.NewString(), 100, uuid.NewString(), nil); err == nil || reached {
		t.Fatal("API override leaked private executor fence", err)
	}
}
