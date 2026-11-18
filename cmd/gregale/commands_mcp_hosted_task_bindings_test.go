package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
)

func TestMCPHostedTaskBindingCLI(t *testing.T) {
	now := time.Now().UTC()
	account, id := uuid.NewString(), strings.Repeat("a", 64)
	record := api.HostedTaskBindingVerification{Version: 1, AccountID: account, IntentID: id, IntentSHA256: strings.Repeat("b", 64), ReviewSHA256: strings.Repeat("c", 64), BindingSHA256: strings.Repeat("d", 64), Status: "selected", Checks: tasktoolbinding.Results("not_checked"), CreatedAt: now, ExpiresAt: now.Add(12 * time.Minute), OwnerAuthorizationRequired: true}
	calls := []string{}
	tamper := false
	var mu sync.Mutex
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v1/account" {
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: account})
			return
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("binding call used generic HTTP replay")
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == "PUT" {
			var in api.HostedTaskBindingSelection
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.IntentSHA256 != record.IntentSHA256 || in.MigrationDatabaseRef != "secret:MIGRATION_URL" {
				t.Error("lost exact selection")
			}
		}
		if strings.HasSuffix(r.URL.Path, "/check") {
			var in api.HostedTaskBindingCheck
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.BindingSHA256 != record.BindingSHA256 {
				t.Error("probe lost selection fingerprint")
			}
			checked := time.Now().UTC()
			until := checked.Add(time.Minute)
			record.Status = "passed"
			record.Checks = tasktoolbinding.Results("passed")
			record.CheckedAt = &checked
			record.ValidUntil = &until
		}
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			record.Status = "revoked"
		}
		out := record
		if tamper {
			out.ReadyToApply = true
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer backend.Close()
	t.Setenv("FAAS_API", backend.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_owner")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("PATH", t.TempDir())
	t.Cleanup(main.ResetCapabilityView)
	flags := []string{"--id", id, "--json"}
	selection := []string{"--intent-sha256", record.IntentSHA256, "--review-sha256", record.ReviewSHA256, "--namespace", "queue", "--web-database-ref", "secret:DATABASE_URL", "--worker-database-ref", "secret:DATABASE_URL", "--migration-database-ref", "secret:MIGRATION_URL", "--observer-database-ref", "secret:DATABASE_URL"}
	run := func(action string, extra []string, want int, status string) {
		t.Helper()
		args := append([]string{"mcp", "server", "tasks", "bindings", action}, flags...)
		args = append(args, extra...)
		code, out, stderr := runCLI(t, args...)
		if code != want {
			t.Fatalf("%s: %d %s %s", action, code, out, stderr)
		}
		if want == 0 {
			var got api.HostedTaskBindingVerification
			if json.Unmarshal([]byte(out), &got) != nil || tasktoolbinding.ValidateReceipt(got, account, id) != nil || got.Status != status {
				t.Fatalf("invalid %s receipt: %s", action, out)
			}
		}
	}
	run("qualify", selection, 0, "passed")
	mu.Lock()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "PUT ") || !strings.HasSuffix(calls[1], "/check") {
		t.Fatal("qualification did not select then check", calls)
	}
	mu.Unlock()
	run("get", nil, 0, "passed")
	run("revoke", []string{"--binding-sha256", record.BindingSHA256}, 0, "revoked")
	mu.Lock()
	tamper = true
	mu.Unlock()
	run("get", nil, 1, "")
}
