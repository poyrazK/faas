package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskRecoveryLifecycleCLIWithoutBindings(t *testing.T) {
	record := taskRetirementCLIRecord(t)
	h := record.Execution.Recovery
	h.Decision = "approved"
	h.AuthorizerKeyID = record.Execution.AuthorizerKeyID
	h.DecidedAt = &h.Qualification.ObservedAt
	if !tasktoolrelease.ValidRecoveryMetadata(record) {
		t.Fatal("invalid retired CLI fixture")
	}
	writes := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
			t.Error("inspection wrote native state")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/v1/task-tool-release-intents/"+record.ID {
			_ = json.NewEncoder(w).Encode(record)
			return
		}
		w.WriteHeader(404)
	}))
	defer backend.Close()
	t.Setenv("FAAS_API", backend.URL)
	t.Setenv("FAAS_TOKEN", "private-token-sentinel")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Cleanup(main.ResetCapabilityView)
	for _, action := range []string{"inspect", "plan-recovery"} {
		code, out, stderr := runCLI(t, "--json", "mcp", "server", "tasks", "execution", action, "--id", record.ID)
		var report tasktoolrelease.RecoveryReport
		if code != 0 || json.Unmarshal([]byte(out), &report) != nil || report.Classification != "retired" || report.NextStep == nil || report.NextStep.Action != "prepare_new_release" || report.NextStep.RequiresOriginalBindings || !report.ReadOnly || report.ReadyToApply || !report.OwnerAuthorizationRequired {
			t.Fatalf("CLI recovery needed private files %d %s %s", code, out, stderr)
		}
		for _, a := range report.Actions {
			if a.Action == "resume" && !slices.Contains(a.Blockers, "executor_permanently_fenced") {
				t.Fatal("CLI can resume fenced executor")
			}
			if a.Action == "retire" && !slices.Contains(a.Blockers, "retirement_already_recorded") {
				t.Fatal("CLI can repeat retirement")
			}
		}
		if strings.Contains(out, "private-token-sentinel") {
			t.Fatal("credential leaked")
		}
	}
	if writes != 0 {
		t.Fatal("CLI inspection mutated workloads")
	}
}
