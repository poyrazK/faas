package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestMCPHostedTaskRehearsalCLI(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	until := now.Add(api.HostedTaskBindingEvidenceTTL)
	account, id := uuid.NewString(), toolpublication.Digest([]byte("cli rehearsal"))
	binding := api.HostedTaskBindingVerification{
		Version: 1, AccountID: account, IntentID: id,
		IntentSHA256: toolpublication.Digest([]byte("intent")), ReviewSHA256: toolpublication.Digest([]byte("review")),
		BindingSHA256: toolpublication.Digest([]byte("binding")), Status: "passed", Checks: tasktoolbinding.Results("passed"),
		CreatedAt: now, ExpiresAt: now.Add(api.HostedTaskSourceRetention), CheckedAt: &now, ValidUntil: &until,
		OwnerAuthorizationRequired: true,
	}
	pins := api.HostedTaskRehearsalPrepare{IntentSHA256: binding.IntentSHA256, ReviewSHA256: binding.ReviewSHA256, BindingSHA256: binding.BindingSHA256}
	record, err := tasktoolrehearsal.New(binding, pins, now)
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	path := "/v1/task-tool-source-intents/" + id + "/rehearsal"
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("rehearsal used generic response replay")
		}
		switch {
		case r.URL.Path == "/v1/account" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: account})
			return
		case r.URL.Path == path && r.Method == http.MethodPut:
			var in api.HostedTaskRehearsalPrepare
			if json.NewDecoder(r.Body).Decode(&in) != nil || in != pins {
				t.Error("rehearsal preparation did not pin the reviewed facts")
			}
		case r.URL.Path == path && r.Method == http.MethodGet:
		case r.URL.Path == path+"/decision" && r.Method == http.MethodPut:
			var in api.HostedTaskRehearsalDecision
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				t.Error("invalid rehearsal decision body")
			}
			updated, err := tasktoolrehearsal.Decide(record, in, owner, now)
			if err != nil {
				t.Error(err)
			}
			record = updated
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(record.Plan)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_hosted_rehearsal_owner")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("PATH", t.TempDir())
	t.Cleanup(main.ResetCapabilityView)
	run := func(action, want string, flags ...string) api.HostedTaskRehearsalPlan {
		t.Helper()
		args := append([]string{"mcp", "server", "tasks", "rehearsal", action, "--id", id, "--json"}, flags...)
		code, output, stderr := runCLI(t, args...)
		var out api.HostedTaskRehearsalPlan
		if code != 0 || json.Unmarshal([]byte(output), &out) != nil || out.Status != want || !out.ExpiresAt.Equal(until) {
			t.Fatalf("%s: %d %s %s", action, code, output, stderr)
		}
		return out
	}
	plan := run("plan", tasktoolrehearsal.Planned, "--intent-sha256", pins.IntentSHA256, "--review-sha256", pins.ReviewSHA256, "--binding-sha256", pins.BindingSHA256)
	flags := []string{"--plan-sha256", plan.PlanSHA256}
	first := run("approve", tasktoolrehearsal.Approved, flags...)
	replay := run("approve", tasktoolrehearsal.Approved, flags...)
	if first.ApprovedAt == nil || replay.ApprovedAt == nil || !first.ApprovedAt.Equal(*replay.ApprovedAt) {
		t.Fatal("approval replay changed its timestamp")
	}
	run("revoke", tasktoolrehearsal.Revoked, flags...)
	run("get", tasktoolrehearsal.Revoked)
}
