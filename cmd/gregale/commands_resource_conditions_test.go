package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	conditionsDatabaseID = "0b5f2d9e-8f43-4c1a-9d65-0a7e1f3c2b11"
	conditionsCronID     = "4c7a1e2b-3d5f-4a6b-8c9d-0e1f2a3b4c5d"
	conditionsRunID      = "7d1e2f3a-4b5c-4d6e-8f9a-0b1c2d3e4f5a"
)

// resourceServer answers each path with its JSON body and everything else
// with a problem 404.
func resourceServer(t *testing.T, routes map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_status")
}

func kindStatus(kind, name string, conditions ...api.Condition) api.ResourceStatus {
	return api.ResourceStatus{Kind: kind, Name: name, ObservedAt: "2026-10-30T15:00:00Z", Conditions: conditions}
}

// postgres, domain and cron status print the kind's conditions; postgres
// resolves a database name to its ID first, and domain status without a
// domain still lists certificates.
func TestResourceStatusCommands(t *testing.T) {
	database := kindStatus("database", "orders",
		api.Condition{Type: "Ready", Status: "True", Reason: "suspended", Message: "Compute scaled to zero; the next connection wakes it."},
		api.Condition{Type: "Progressing", Status: "False", Reason: "complete", Message: "Nothing is in progress."},
		api.Condition{Type: "Degraded", Status: "False", Reason: "healthy", Message: "fine"})
	domain := kindStatus("domain", "shop.example.com",
		api.Condition{Type: "Ready", Status: "False", Reason: "dns_pending", Message: "Ownership is not verified yet."},
		api.Condition{Type: "Verified", Status: "False", Reason: "dns_pending", Message: "Ownership is not verified yet."},
		api.Condition{Type: "CertificateReady", Status: "False", Reason: "pending", Message: "Issued after verification."})
	cron := kindStatus("cron", conditionsCronID,
		api.Condition{Type: "Ready", Status: "True", Reason: "scheduled", Message: "The cron fires on schedule @daily."},
		api.Condition{Type: "LastRunSucceeded", Status: "Unknown", Reason: "never_run", Message: "The cron has not run yet."})
	resourceServer(t, map[string]any{
		"/v1/postgres/databases": api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: conditionsDatabaseID, Name: "orders"}}},
		"/v1/postgres/databases/" + conditionsDatabaseID + "/status": database,
		"/v1/domains/shop.example.com/status":                        domain,
		"/v1/crons/" + conditionsCronID + "/status":                  cron,
		"/v1/domains": []api.CustomDomainResponse{{Domain: "shop.example.com", CertStatus: "pending"}},
	})
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"postgres", "status", "orders"}, []string{"database orders", "Ready", "suspended", "Degraded"}},
		{[]string{"domain", "status", "shop.example.com"}, []string{"domain shop.example.com", "Verified", "dns_pending", "CertificateReady"}},
		// The listing writes to the process stdout; its exit code shows it ran.
		{[]string{"domain", "status"}, nil},
		{[]string{"cron", "status", conditionsCronID}, []string{"cron " + conditionsCronID, "LastRunSucceeded", "never_run"}},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr := captureOutput(t)
			if code := main.Run(tc.args); code != 0 {
				t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output missing %q:\n%s", want, stdout)
				}
			}
		})
	}
}

func TestResourceWaitCommands(t *testing.T) {
	succeeded := kindStatus("job_run", conditionsRunID,
		api.Condition{Type: "Succeeded", Status: "True", Reason: "succeeded", Message: "All 1 tasks succeeded."},
		api.Condition{Type: "Progressing", Status: "False", Reason: "complete", Message: "The run finished."})
	disabled := kindStatus("cron", conditionsCronID,
		api.Condition{Type: "Ready", Status: "False", Reason: "disabled", Message: "The cron is disabled.", Terminal: true},
		api.Condition{Type: "LastRunSucceeded", Status: "Unknown", Reason: "never_run", Message: "The cron has not run yet."})
	resourceServer(t, map[string]any{
		"/v1/jobs/nightly": api.JobResponse{ID: "j1", Name: "nightly", AccountID: "a1"},
		"/v1/jobs/nightly/runs/" + conditionsRunID + "/status": succeeded,
		"/v1/crons/" + conditionsCronID + "/status":            disabled,
	})
	tests := []struct {
		name      string
		args      []string
		code      int
		wantError string
	}{
		{"job wait --for holds", []string{"job", "wait", "nightly", conditionsRunID, "--for", "Succeeded"}, 0, ""},
		{"job wait --for rejects Ready", []string{"job", "wait", "nightly", conditionsRunID, "--for", "Ready"}, 1, "job_run conditions are Succeeded, Progressing"},
		{"job wait --for rejects --out-dir", []string{"job", "wait", "nightly", conditionsRunID, "--for", "Succeeded", "--out-dir", t.TempDir()}, 1, "drop --out-dir"},
		{"cron wait stops on terminal", []string{"cron", "wait", conditionsCronID}, 1, "will not change without a new request"},
		{"cron wait rejects a bad id", []string{"cron", "wait", "nightly"}, 1, "not a cron id"},
		{"domain wait not found", []string{"domain", "wait", "missing.example.com"}, 4, "gregale domain get missing.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr := captureOutput(t)
			if code := main.Run(append(tc.args, "--poll-interval", "1s")); code != tc.code {
				t.Errorf("exit = %d, want %d; stderr:\n%s", code, tc.code, stderr)
			}
			if tc.wantError != "" && !strings.Contains(stderr.String(), tc.wantError) {
				t.Errorf("stderr missing %q:\n%s", tc.wantError, stderr)
			}
		})
	}
}

func TestJobStatusConditions(t *testing.T) {
	resourceServer(t, map[string]any{
		"/v1/jobs/nightly": api.JobResponse{ID: "j1", Name: "nightly", AccountID: "a1"},
		"/v1/jobs/nightly/runs/" + conditionsRunID + "/status": kindStatus("job_run", conditionsRunID,
			api.Condition{Type: "Succeeded", Status: "False", Reason: "running", Message: "1 of 2 tasks succeeded."},
			api.Condition{Type: "Progressing", Status: "True", Reason: "running", Message: "1 of 2 tasks succeeded."}),
	})
	stdout, stderr := captureOutput(t)
	if code := main.Run([]string{"job", "status", "nightly", conditionsRunID, "--conditions"}); code != 0 {
		t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"job_run " + conditionsRunID, "Succeeded", "running"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}
