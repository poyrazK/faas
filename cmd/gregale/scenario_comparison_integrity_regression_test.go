package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func compareIntegrityReports(t *testing.T, before, after []main.TestRunReceipt, budget string) (int, string) {
	t.Helper()
	out, stderr := main.CaptureSuiteTestOutput(t)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")}
	for i, rows := range [][]main.TestRunReceipt{before, after} {
		body, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths[i], body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	budgetPath := filepath.Join(dir, "budget.yaml")
	if err := os.WriteFile(budgetPath, []byte(budget), 0o600); err != nil {
		t.Fatal(err)
	}
	code := main.CmdTestCompare([]string{paths[0], paths[1], "--budget", budgetPath})
	if stderr.Len() != 0 {
		t.Fatalf("comparison error: %s", stderr.String())
	}
	var result struct {
		Gate struct {
			Status string `json:"status"`
		} `json:"gate"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid comparison output: %v: %s", err, out.String())
	}
	return code, result.Gate.Status
}

func completeComparisonReceipt() main.TestRunReceipt {
	return main.ComparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")
}

func TestComparisonRejectsIncompleteLoadEvidence(t *testing.T) {
	for _, repeated := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			mutate func(*main.TestRunReceipt)
		}{
			{"interrupted", func(r *main.TestRunReceipt) {
				r.Status = "failed"
				r.Error = "load canceled"
				r.Load.Status = "failed"
				r.Load.StopReason = "canceled"
				r.Load.IterationsCompleted--
				r.Load.IterationsInterrupted++
			}},
			{"canceled after completion", func(r *main.TestRunReceipt) { r.Load.StopReason = "canceled" }},
			{"missing all steps", func(r *main.TestRunReceipt) { r.Load.Steps = nil }},
			{"aggregate count mismatch", func(r *main.TestRunReceipt) { r.Load.Steps[0].Requests--; r.Load.Steps[0].Latency.Samples-- }},
		} {
			for _, side := range []string{"earlier", "later"} {
				t.Run(fmt.Sprintf("repeated=%t/%s/%s", repeated, tc.name, side), func(t *testing.T) {
					before := []main.TestRunReceipt{completeComparisonReceipt()}
					after := []main.TestRunReceipt{completeComparisonReceipt()}
					budget := "version: 1\nruns:\n  require_passing: false\nload:\n  max_p95_increase_percent: 10\n"
					if repeated {
						old, next := completeComparisonReceipt(), completeComparisonReceipt()
						old.Attempt, next.Attempt = 2, 2
						before, after = append(before, old), append(after, next)
						budget += "repeat:\n  min_runs: 2\n"
					}
					if side == "earlier" {
						tc.mutate(&before[0])
					} else {
						tc.mutate(&after[0])
					}
					if code, status := compareIntegrityReports(t, before, after, budget); code == 0 || status != "failed" {
						t.Fatalf("invalid evidence accepted: exit %d gate %s", code, status)
					}
				})
			}
		}
	}
	for _, repeated := range []bool{false, true} {
		t.Run(fmt.Sprintf("valid/repeated=%t", repeated), func(t *testing.T) {
			before, after := []main.TestRunReceipt{completeComparisonReceipt()}, []main.TestRunReceipt{completeComparisonReceipt()}
			budget := "version: 1\nload:\n  max_p95_increase_percent: 10\n"
			if repeated {
				old, next := completeComparisonReceipt(), completeComparisonReceipt()
				old.Attempt, next.Attempt = 2, 2
				before, after = append(before, old), append(after, next)
				budget += "repeat:\n  min_runs: 2\n"
			}
			if code, status := compareIntegrityReports(t, before, after, budget); code != 0 || status != "passed" {
				t.Fatalf("complete evidence rejected: exit %d gate %s", code, status)
			}
		})
	}
}

func TestComparisonDetectsHTTPQueryTemplateChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	receipt := func(t *testing.T, query string) main.TestRunReceipt {
		t.Helper()
		step := main.TestHTTPRequest{Name: "export", Method: "GET", Path: "/export?" + query, Expect: main.TestHTTPExpect{Status: 200}}
		evidence, err := main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, []main.TestHTTPRequest{step}, nil)
		if err != nil {
			t.Fatal(err)
		}
		evidence[0].DurationMS = 10
		body, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), query) || evidence[0].PathTemplateSHA256 == "" {
			t.Fatalf("query disclosed or identity absent: %s", body)
		}
		return main.TestRunReceipt{Scenario: "export", Engine: "local", Profile: "local", Attempt: 1, Status: "passed", Requests: evidence}
	}
	for _, repeated := range []bool{false, true} {
		for _, variant := range []string{"unchanged", "changed", "legacy", "changed within attempts"} {
			if !repeated && variant == "changed within attempts" {
				continue
			}
			t.Run(fmt.Sprintf("repeated=%t/%s", repeated, variant), func(t *testing.T) {
				before := []main.TestRunReceipt{receipt(t, "format=csv")}
				after := []main.TestRunReceipt{receipt(t, "format=csv")}
				if variant == "changed" {
					after[0] = receipt(t, "format=pdf")
				}
				if variant == "legacy" {
					before[0].Requests[0].PathTemplateSHA256 = ""
				}
				budget := "version: 1\nhttp:\n  max_duration_increase_percent: 10\n"
				if repeated {
					old, next := receipt(t, "format=csv"), receipt(t, "format=csv")
					if variant == "changed within attempts" {
						next = receipt(t, "format=pdf")
					}
					old.Attempt, next.Attempt = 2, 2
					before, after = append(before, old), append(after, next)
					budget += "repeat:\n  min_runs: 2\n"
				}
				code, status := compareIntegrityReports(t, before, after, budget)
				if variant == "unchanged" && (code != 0 || status != "passed") {
					t.Fatalf("matching query rejected: %d %s", code, status)
				}
				if variant != "unchanged" && (code == 0 || status != "failed") {
					t.Fatalf("changed or missing identity accepted: %d %s", code, status)
				}
			})
		}
	}
}
