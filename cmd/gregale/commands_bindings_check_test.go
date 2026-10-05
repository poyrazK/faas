// adr: 427 — one inventory GET produces a policy report and CI exit status.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

func bindingCheckCLIInventory() api.AppBindingInventory {
	now, checked, stamp := time.Now().UTC(), time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(-time.Hour)
	pending := false
	return api.AppBindingInventory{App: "api", Complete: true, GeneratedAt: now, VerificationDeploymentID: "deployment-1", VerificationScope: "production",
		Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeObjectStorage, Name: "assets", Binding: "ASSETS", Scope: "production", State: "active", RotationPending: &pending,
			VerificationStatus: "passed", Verification: &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: "deployment-1", Scope: "production", CheckedAt: &checked,
				Checks: []api.BindingVerificationCheck{{Name: "environment", Status: "passed"}, {Name: "configuration", Status: "passed"}, {Name: "connection", Status: "passed"},
					{Name: "authorization", Status: "passed"}, {Name: "bucket_access", Status: "passed"}}}}},
		RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: now, ConfigChangedAt: &stamp,
			Deployments: []api.BindingRuntimeDeployment{{DeploymentID: "deployment-1", Scope: "production", DeploymentStatus: "live", Status: "current",
				Serving: api.BindingRuntimeInstanceCounts{Current: 1}, Resident: api.BindingRuntimeInstanceCounts{Current: 1}}}}}
}

func runBindingCheckCLI(t *testing.T, inventory api.AppBindingInventory, scope string, args ...string) (int, string, string) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/bindings" || r.URL.Query().Get("scope") != scope {
			t.Errorf("preflight must use one read: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inventory)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var stdout, stderr bytes.Buffer
	osStdout, osStderr, jsonOutput = &stdout, &stderr, false
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	code := run(args)
	if calls != 1 {
		t.Fatalf("requests=%d, want one inventory GET; output=%s %s", calls, stdout.String(), stderr.String())
	}
	return code, stdout.String(), stderr.String()
}

func decodeBindingCheckCLI(t *testing.T, stdout string) bindingcheck.Report {
	t.Helper()
	var report bindingcheck.Report
	decoder := json.NewDecoder(strings.NewReader(stdout))
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("preflight report is not JSON: %v %s", err, stdout)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("extra JSON output: %s", stdout)
	}
	return report
}

func bindingCheckCLICodes(report bindingcheck.Report) []string {
	result := []string{}
	for _, blocker := range report.Blockers {
		result = append(result, blocker.Code)
	}
	return result
}

func TestCmdBindingsCheckUsesOneInventoryReadAndJSONExitStatus(t *testing.T) {
	for _, args := range [][]string{
		{"bindings", "check", "api", "--json"},
		{"--json", "bindings", "check", "--max-verification-age", "5m", "--scope", "production", "api"},
		{"bindings", "check", "api", "--scope=production", "--allow-unsupported=false", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			inventory := bindingCheckCLIInventory()
			if strings.Contains(strings.Join(args, " "), "--scope") {
				inventory.Scope = "production"
			}
			code, stdout, stderr := runBindingCheckCLI(t, inventory, inventory.Scope, args...)
			report := decodeBindingCheckCLI(t, stdout)
			if code != 0 || !report.Passed || report.Scope != "production" || report.DeploymentID != "deployment-1" || report.Coverage != "complete" || stderr != "" {
				t.Fatalf("exit=%d report=%+v stderr=%s", code, report, stderr)
			}
			if strings.Contains(strings.Join(args, " "), "5m") && report.MaxVerificationAge != "5m0s" {
				t.Fatalf("custom verification age ignored: %+v", report)
			}
		})
	}
}

func TestCmdBindingsCheckBlocksUnsafeRecordedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(*api.AppBindingInventory)
	}{
		{"failed probe", "verification_failed", func(i *api.AppBindingInventory) { i.Bindings[0].VerificationStatus = "failed" }},
		{"old evidence", "verification_expired", func(i *api.AppBindingInventory) {
			at := time.Now().Add(-11 * time.Minute)
			i.Bindings[0].Verification.CheckedAt = &at
		}},
		{"new deployment", "verification_deployment_mismatch", func(i *api.AppBindingInventory) { i.Bindings[0].Verification.DeploymentID = "previous-deployment" }},
		{"missing evidence", "verification_unknown", func(i *api.AppBindingInventory) { i.Bindings[0].Verification = nil }},
		{"failed refresh", "refresh_failed", func(i *api.AppBindingInventory) { i.Bindings[0].Refresh = &api.BindingRefresh{Status: "failed"} }},
		{"pending retirement despite refresh", "rotation_pending", func(i *api.AppBindingInventory) {
			pending := true
			i.Bindings[0].RotationPending = &pending
			i.Bindings[0].Refresh = &api.BindingRefresh{Status: "completed"}
		}},
		{"unreadable optional domain", "inventory_incomplete", func(i *api.AppBindingInventory) {
			i.Complete = false
			i.Issues = []api.BindingInventoryIssue{{Type: "postgres", Code: "managed_postgres_unavailable", Severity: "warning"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory := bindingCheckCLIInventory()
			tc.mutate(&inventory)
			code, stdout, _ := runBindingCheckCLI(t, inventory, "", "bindings", "check", "api", "--json")
			report := decodeBindingCheckCLI(t, stdout)
			if code != 1 || report.Passed || !slices.Contains(bindingCheckCLICodes(report), tc.code) {
				t.Fatalf("exit=%d report=%+v, want %s", code, report, tc.code)
			}
		})
	}
}

func TestCmdBindingsCheckPassingProbeStillBlocksOldServingInstance(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			inventory := bindingCheckCLIInventory()
			inventory.Bindings[0].Refresh = &api.BindingRefresh{Status: "completed"}
			d := &inventory.RuntimeFreshness.Deployments[0]
			d.Serving, d.Resident, d.Status = api.BindingRuntimeInstanceCounts{Stale: 1}, api.BindingRuntimeInstanceCounts{Stale: 1}, "stale"
			args := []string{"bindings", "check", "api"}
			if asJSON {
				args = append(args, "--json")
			}
			code, stdout, stderr := runBindingCheckCLI(t, inventory, "", args...)
			if code != 1 || !strings.Contains(stdout, "runtime_stale") || stderr != "" {
				t.Fatalf("exit=%d output=%s %s", code, stdout, stderr)
			}
			if asJSON {
				report := decodeBindingCheckCLI(t, stdout)
				if report.Passed || report.Runtime[0].Serving.Stale != 1 {
					t.Fatalf("old serving instance hidden: %+v", report)
				}
			} else {
				for _, want := range []string{"Bindings preflight blocked", "serving current=0 stale=1", "resident credential use", "bucket-list read access only"} {
					if !strings.Contains(stdout, want) {
						t.Fatalf("missing %q: %s", want, stdout)
					}
				}
			}
		})
	}
}

func TestCmdBindingsCheckScopeAssertAndUnsupportedWaiver(t *testing.T) {
	t.Run("scope assertion", func(t *testing.T) {
		inventory := bindingCheckCLIInventory()
		inventory.Scope = "staging"
		code, stdout, _ := runBindingCheckCLI(t, inventory, "staging", "bindings", "check", "api", "--scope", "staging", "--json")
		report := decodeBindingCheckCLI(t, stdout)
		if code != 1 || report.Passed || !slices.Contains(bindingCheckCLICodes(report), "deployment_scope_mismatch") {
			t.Fatalf("other scope accepted: exit=%d %+v", code, report)
		}
	})
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "waived"}[allow], func(t *testing.T) {
			inventory := bindingCheckCLIInventory()
			inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Access: "pull", Scope: "app", State: "enabled"})
			args := []string{"bindings", "check", "api", "--json"}
			if allow {
				args = append(args, "--allow-unsupported")
			}
			code, stdout, _ := runBindingCheckCLI(t, inventory, "", args...)
			report := decodeBindingCheckCLI(t, stdout)
			want := 1
			if allow {
				want = 0
			}
			if code != want || report.Passed != allow || report.Coverage != "partial" || report.Bindings[1].Status != "unsupported" || report.AllowUnsupported != allow {
				t.Fatalf("coverage waiver exit=%d report=%+v", code, report)
			}
		})
	}
}

func TestCmdBindingsCheckQueueReadinessBlocksWaivedJSONAndHumanOutput(t *testing.T) {
	for _, json := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[json], func(t *testing.T) {
			inventory := bindingCheckCLIInventory()
			polled := time.Now().Add(-time.Minute)
			inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "worker", Scope: "app", State: "enabled",
				Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &polled})
			args := []string{"bindings", "check", "api", "--allow-unsupported", "--max-verification-age", "1h"}
			if json {
				args = append(args, "--json")
			}
			code, stdout, _ := runBindingCheckCLI(t, inventory, "", args...)
			if code != 1 {
				t.Fatalf("waiver hid stale consumer: exit=%d %s", code, stdout)
			}
			if json {
				report := decodeBindingCheckCLI(t, stdout)
				if report.Passed || report.Coverage != "partial" || !slices.Contains(bindingCheckCLICodes(report), "queue_consumer_stale") {
					t.Fatalf("missing queue blocker: %+v", report)
				}
			} else {
				for _, want := range []string{"queue orders (worker) scope=app: blocked", "queue_consumer_stale", "scheduler polling", "verification_unsupported"} {
					if !strings.Contains(stdout, want) {
						t.Fatalf("missing %q: %s", want, stdout)
					}
				}
			}
		})
	}
}

func TestCmdBindingsCheckRejectsInvalidPolicyBeforeRequest(t *testing.T) {
	for _, args := range [][]string{
		{}, {"api", "extra"}, {"../bad"}, {"api", "--scope", "__all__"},
		{"api", "--max-verification-age", "0"}, {"api", "--max-verification-age", "-1s"}, {"api", "--max-verification-age", "tomorrow"},
		{"api", "--max-verification-age"}, {"api", "--deployment", "invalid"}, {"api", "--deployment", "v0"}, {"api", "--allow-unsupported=maybe"}, {"api", "--unexpected"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) }))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var output bytes.Buffer
			osStdout, osStderr, jsonOutput = &output, &output, false
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			if code := run(append([]string{"bindings", "check"}, args...)); code != 1 || calls != 0 {
				t.Fatalf("invalid policy made request: exit=%d calls=%d output=%s", code, calls, output.String())
			}
		})
	}
}

func TestCmdBindingsCheckInventoryRequestFailureCannotPass(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":403,"title":"Forbidden","code":"forbidden"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var output bytes.Buffer
	osStdout, osStderr, jsonOutput = &output, &output, false
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	if code := run([]string{"bindings", "check", "api"}); code != 1 || calls != 1 || !strings.Contains(output.String(), "Forbidden") {
		t.Fatalf("request failure exit=%d calls=%d output=%s", code, calls, output.String())
	}
}

func TestBindingsCheckApplicationAckRejectsOldInventoryAndShowsCounts(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprint(current), func(t *testing.T) {
			inventory := bindingCheckCLIInventory()
			if current {
				now := inventory.GeneratedAt
				adoption := &api.BindingApplicationAdoption{Source: "application_ack", Complete: true, ObservedAt: now, SecretsExpected: 6, SecretsObserved: 6}
				for _, key := range api.BindingCredentialSecretKeys(api.BindingTypeObjectStorage, "ASSETS") {
					adoption.Targets = append(adoption.Targets, api.BindingApplicationAckTarget{DeploymentID: "deployment-1", InstanceID: "runtime", RuntimeState: "running", Key: key, CurrentVersion: 1, ReloadSupport: "enabled", ReloadVersion: 1, Projection: "updated", Signal: "sent", ReloadAt: &now, ApplicationAckVersion: 1, ApplicationAck: "applied", ApplicationAckAt: &now, ProcessGeneration: strings.Repeat("a", 32), ApplicationAckGeneration: strings.Repeat("a", 32)})
				}
				inventory.Bindings[0].ApplicationAdoption = adoption
			}
			code, out, errs := runBindingCheckCLI(t, inventory, "", "bindings", "check", "api", "--require-application-ack")
			if (code == 0) != current || current && !strings.Contains(out, "current=6") || !current && !strings.Contains(out, "application_adoption_unknown") {
				t.Fatalf("current=%t code=%d %s %s", current, code, out, errs)
			}
		})
	}
}
