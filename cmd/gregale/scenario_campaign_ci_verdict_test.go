package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCampaignStatusCIVerdict(t *testing.T) {
	// adr: 984 — a clean resource inventory cannot waive execution, policy, or evidence failures.
	for _, mode := range []string{"pass", "missing", "failed", "policy", "present", "unverified", "changed", "lineage", "empty", "partial_recovery", "recovered", "recorded_failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			r := testCampaignStatusReport{Side: "current", SnapshotStable: true, LineageComplete: true, PolicyPassed: true,
				Batches:          []testCampaignStatusBatch{{ID: "a", Status: "completed", Evidence: "verified_receipts", Cleanup: "recorded_clean", ExecutedRuns: 1, PassedRuns: 1, Estimated: testCampaignPlanBudget{Runs: 1}}},
				LiveVerification: &testCampaignStatusLiveReport{SnapshotStable: true, AllVerified: true, ResourcesClean: true, State: "verified_clean", Resources: []testCampaignStatusLiveResource{{Kind: "consumer", State: "cancelled", VerifiedAt: &now}}}}
			switch mode {
			case "missing":
				r.Batches[0].Status = "pending"
			case "failed":
				r.Batches[0].ExecutionFailed = true
				r.Batches[0].PassedRuns = 0
			case "policy":
				r.PolicyPassed = false
				r.PolicyViolations = 1
			case "present":
				r.LiveVerification.State = "verified_resources_present"
				r.LiveVerification.ResourcesClean = false
				r.LiveVerification.Resources[0].State = "active"
			case "unverified":
				r.LiveVerification.AllVerified = false
			case "changed":
				r.SnapshotStable = false
			case "lineage":
				r.LineageComplete = false
			case "empty":
				r.Batches = nil
			case "partial_recovery":
				r.Batches[0].Status = "recovered"
				r.Batches[0].Evidence = "verified_recovery_bundle"
				r.Batches[0].ExecutedRuns = 0
			case "recovered":
				r.Batches[0].Status = "recovered"
				r.Batches[0].Evidence = "verified_recovery_bundle"
			case "recorded_failure":
				r.Batches[0].Cleanup = "recorded_failure"
			case "cancelled":
				r.CleanWait = &testCampaignStatusCleanWait{Outcome: "cancelled"}
			}
			v := buildTestCampaignCIVerdict(r)
			want := mode == "pass" || mode == "recovered"
			if v.Passed != want || (v.ExitCode == 0) != want {
				t.Fatalf("unexpected verdict: %+v", v)
			}
			if mode == "failed" && (v.Cleanup.State != "passed" || v.Execution.State != "failed") {
				t.Fatal("cleanup concealed failed test", v)
			}
			if mode == "policy" && (v.Execution.State != "passed" || v.Cleanup.State != "passed" || v.Policy.State != "failed") {
				t.Fatal("checks were conflated", v)
			}
		})
	}
}

func TestCampaignStatusCIVerifyCLI(t *testing.T) {
	// adr: 984 — the public combined command checks bound receipts and fresh resources before exporting its verdict.
	for _, mode := range []string{"pass", "wait", "failed", "missing", "policy", "present", "unverified", "changed_report", "changed_baseline"} {
		t.Run(mode, func(t *testing.T) {
			f := newCampaignFaultHistoryFixture(t)
			plan := f.plan
			plan.Recovery = nil
			dir := f.dirs[0]
			for _, name := range []string{testCampaignRecoverySealName, testCampaignContinuationOwnerName} {
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			receipt := campaignRecoveryTestReceipt(f.scenarios["checkout"], strings.Repeat("1", 32))
			receipt.WorkloadAppIDs = map[string]string{"checkout": "app-0"}
			if mode == "failed" {
				receipt.Status = "failed"
			}
			reportPath := filepath.Join(dir, "batch-a.json")
			mustCampaignFaultJSON(t, reportPath, []testRunReceipt{receipt})
			_, hash, err := readTestBaselineReport(reportPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			baselinePath := filepath.Join(f.root, "baseline.json")
			baseline := campaignRecoveryTestReceipt(f.scenarios["checkout"], "ci-baseline")
			mustCampaignFaultJSON(t, baselinePath, []testRunReceipt{baseline})
			_, plan.Execution.BaselineSHA256, err = readTestBaselineReport(baselinePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "policy" {
				plan.Execution.BaselineSHA256 = ""
			}
			planPath := filepath.Join(f.root, "plan.json")
			mustCampaignFaultJSON(t, planPath, plan)
			var cp testCampaignCheckpoint
			if err := readTestCampaignJSON(filepath.Join(dir, "checkpoint.json"), &cp); err != nil {
				t.Fatal(err)
			}
			cp.PlanSHA256, cp.Recovery, cp.Halted = testCampaignJSONHash(plan), nil, false
			cp.Batches["a"] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
			if mode == "missing" {
				cp.Batches["a"] = testCampaignCheckpointBatch{Status: "uncertain"}
				if err := os.Remove(reportPath); err != nil {
					t.Fatal(err)
				}
			}
			mustCampaignFaultJSON(t, filepath.Join(dir, "checkpoint.json"), cp)
			bindingPath := filepath.Join(testCampaignJournalDirectory(dir, "a"), "binding.json")
			var binding testCampaignJournalBinding
			if err := readTestCampaignJSON(bindingPath, &binding); err != nil {
				t.Fatal(err)
			}
			binding.PlanSHA256 = cp.PlanSHA256
			mustCampaignFaultJSON(t, bindingPath, binding)
			journalPath := filepath.Join(testCampaignJournalDirectory(dir, "a"), strings.Repeat("1", 32)+".json")
			var journal testCampaignRunJournal
			if err := readTestCampaignJSON(journalPath, &journal); err != nil {
				t.Fatal(err)
			}
			journal.Binding = binding
			mustCampaignFaultJSON(t, journalPath, journal)
			if _, err := collectTestCampaignFaultHistory(plan, cp, f.scenarios, dir, nil); err != nil {
				t.Fatal("invalid verification fixture", err)
			}
			backend := newCampaignFaultHistoryAPI(t, f)
			if mode != "present" {
				for id, c := range backend.consumers {
					c.Status = "revoked"
					backend.consumers[id] = c
				}
				for run := range backend.registrations {
					backend.registrations[run] = map[string]string{}
				}
				backend.sessions = map[string]string{}
			}
			var requests, mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" {
					mutations.Add(1)
					http.Error(w, "unexpected mutation", 405)
					return
				}
				if requests.Load() == 1 && strings.HasPrefix(mode, "changed_") {
					path := reportPath
					if mode == "changed_baseline" {
						path = baselinePath
					}
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Error(err)
						return
					}
					if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
						t.Error(err)
						return
					}
				}
				if mode == "unverified" {
					http.Error(w, "unavailable", 503)
					return
				}
				fields := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
				var value any
				var err error
				switch {
				case len(fields) == 4 && fields[2] == "test-runs":
					value, err = backend.GetScenarioTestRegistration(r.Context(), fields[3])
				case len(fields) == 5 && fields[3] == "consumers":
					value, err = backend.GetAPIConsumer(r.Context(), fields[2], fields[4])
				case len(fields) == 4 && fields[2] == "sessions":
					value, err = backend.GetDevSession(r.Context(), fields[3], r.URL.Query().Get("workspace_id"))
				default:
					http.NotFound(w, r)
					return
				}
				if err != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "ci-test")
			frozen := campaignFaultFrozenBytes(t, []string{f.root})
			output, html := filepath.Join(t.TempDir(), "ci.json"), filepath.Join(t.TempDir(), "ci.html")
			args := []string{"verify", "--plan", planPath, "--policy", filepath.Join(f.root, "policy.yaml"), "--manifest", filepath.Join(f.root, "manifest.yaml"), "--directory", dir, "--json", output, "--html", html, "--verify-live=false", "--require-clean=false"}
			if mode != "policy" {
				args = append(args, "--baseline", baselinePath)
			}
			if mode == "wait" {
				args = append(args, "--wait-clean", "--timeout", "1s", "--interval", "1s")
			}
			want := testCampaignCIVerdictFailed
			if mode == "pass" || mode == "wait" {
				want = 0
			}
			if code := cmdTestPolicy(args); code != want {
				t.Fatalf("exit %d want %d", code, want)
			}
			var result testCampaignStatusReport
			if err := readTestCampaignJSON(output, &result); err != nil {
				t.Fatal(err)
			}
			v := result.CIVerdict
			if v == nil || v.ExitCode != want || result.Informational || !result.ReadOnly || requests.Load() == 0 || mutations.Load() != 0 {
				t.Fatal("combined verification bypassed checks or lost report", v)
			}
			if mode == "failed" && (v.Execution.State != "failed" || v.Cleanup.State != "passed") {
				t.Fatal("failed receipts hidden by clean resources", v)
			}
			if strings.HasPrefix(mode, "changed_") && (result.SnapshotStable || v.Execution.State != "incomplete" || v.Policy.State != "incomplete") {
				t.Fatal("changed evidence trusted", v)
			}
			body, err := os.ReadFile(html)
			if err != nil || !bytes.Contains(body, []byte("CI verdict")) {
				t.Fatal("CI HTML missing", err)
			}
			if !strings.HasPrefix(mode, "changed_") {
				assertCampaignFaultFrozen(t, frozen, []string{f.root})
			}
		})
	}
}

func TestCampaignStatusCIPassedEvidence(t *testing.T) {
	// adr: 984 — recorded cleanup failures and failed wrapper receipts survive recovery adoption.
	r := campaignRecoveryTestReceipt(testScenario{Project: "checkout"}, "ci-proof")
	if passed, failed := testCampaignPassedRuns([]testRunReceipt{r}); passed != 1 || failed {
		t.Fatal("passing evidence rejected")
	}
	r.CleanupError = "original cleanup failed"
	if passed, failed := testCampaignPassedRuns([]testRunReceipt{r}); passed != 1 || !failed {
		t.Fatal("cleanup history was discarded")
	}
	r.CleanupError = ""
	root := testRunReceipt{Status: "failed", Resilience: &testResilienceEvidence{Runs: 1, History: []testRunReceipt{r}}}
	if passed, failed := testCampaignPassedRuns([]testRunReceipt{root}); passed != 1 || !failed {
		t.Fatal("failed wrapper was discarded")
	}
}
