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
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

func TestScenarioReportsProtectDeclaredBehaviorFiles(t *testing.T) {
	for _, suite := range []bool{false, true} {
		for _, input := range []string{"fixture.json", "observe.sh"} {
			for _, output := range []string{"--report", "--junit", "--html"} {
				for _, alias := range []string{"direct", "symlink", "hardlink"} {
					t.Run(fmt.Sprintf("suite=%t/%s/%s/%s", suite, input, output, alias), func(t *testing.T) {
						dir := t.TempDir()
						app := filepath.Join(dir, "app")
						if err := os.Mkdir(app, 0o700); err != nil {
							t.Fatal(err)
						}
						main.CaptureSuiteTestOutput(t)
						main.WriteSuiteTestFile(t, filepath.Join(app, "fixture.json"), `{"expected":true}`)
						main.WriteSuiteTestFile(t, filepath.Join(app, "observe.sh"), `printf ran > started
printf '{"version":1,"observations":[{"key":"audit.ok","value":true}]}' > "$GREGALE_TEST_BEHAVIOR_OUTPUT"
`)
						scenario := main.TestScenario{Project: "audit-app", Source: "app", Command: []string{"sh", "observe.sh"}, Simulation: []string{"sh", "observe.sh"},
							Behavior: &main.TestBehaviorSpec{Version: 1, Fixtures: []string{"fixture.json"}, Observers: []string{"observe.sh"}, Observations: []behaviorcompare.Spec{{Key: "audit.ok", Kind: "http"}}},
						}
						first := main.TestScenario{Project: "first-app", Source: "app", Command: []string{"true"}, Simulation: []string{"sh", "-c", "printf ran > first-started"}}
						manifest := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1,
							Scenarios: map[string]main.TestScenario{"first": first, "audit": scenario},
							Suites: map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{
								{Scenario: "first", Engine: "simulated"}, {Scenario: "audit", Engine: "simulated"},
							}}},
						})
						inputPath := filepath.Join(app, input)
						before, err := os.ReadFile(inputPath)
						if err != nil {
							t.Fatal(err)
						}
						outputPath := inputPath
						if alias != "direct" {
							outputPath = filepath.Join(dir, "output")
							link := os.Symlink
							if alias == "hardlink" {
								link = os.Link
							}
							if err := link(inputPath, outputPath); err != nil {
								t.Fatal(err)
							}
						}
						args := []string{"--manifest", manifest, output, outputPath}
						if suite {
							args = append(args, "--suite", "smoke")
						} else {
							args = append(args, "--scenario", "audit", "--engine", "simulated")
						}
						code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) })
						if code == 0 || !strings.Contains(problem, "would overwrite test input") {
							t.Errorf("declared input collision exit %d: %s", code, problem)
						}
						after, err := os.ReadFile(inputPath)
						if err != nil || string(after) != string(before) {
							t.Errorf("declared input changed: %v", err)
						}
						for _, name := range []string{"started", "first-started"} {
							if _, err := os.Stat(filepath.Join(app, name)); !os.IsNotExist(err) {
								t.Errorf("scenario ran before rejecting the collision: %s", name)
							}
						}
					})
				}
			}
		}
	}
}

func TestScenarioMinimumEvidenceBudgetsApplyWithoutMetricThresholds(t *testing.T) {
	for _, tc := range []struct {
		name, budget      string
		attempts, samples int
		pass              bool
		rareEndpoint      bool
	}{
		{"too few attempts", "repeat: {min_runs: 3}", 1, 100, false, false},
		{"enough attempts", "repeat: {min_runs: 3}", 3, 100, true, false},
		{"too few samples", "load: {min_samples: 200}", 1, 100, false, false},
		{"enough samples", "load: {min_samples: 200}", 1, 200, true, false},
		{"repeat samples sparse", "repeat: {min_runs: 3}\nload: {min_samples: 200}", 3, 100, false, false},
		{"repeat samples sufficient", "repeat: {min_runs: 3}\nload: {min_samples: 200}", 3, 200, true, false},
		{"sparse endpoint", "load: {min_samples: 200}", 1, 400, false, true},
		{"repeated sparse endpoint", "repeat: {min_runs: 3}\nload: {min_samples: 200}", 3, 400, false, true},
		{"status budget needs no load", "runs: {require_passing: true}", 1, 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stdout, _ := main.CaptureSuiteTestOutput(t)
			receipts := make([]main.TestRunReceipt, tc.attempts)
			for i := range receipts {
				receipts[i] = main.ComparisonBudgetReceipt(10, 15, 0, tc.samples, 100, 10, "passed")
				receipts[i].Attempt = i + 1
				if tc.samples == 0 {
					receipts[i].Load = nil
				}
				if tc.rareEndpoint {
					rare := main.ComparisonBudgetReceipt(10, 15, 0, 20, 100, 10, "passed")
					common := main.ComparisonBudgetReceipt(10, 15, 0, tc.samples-20, 100, 10, "passed")
					receipts[i].Load.Steps = append(rare.Load.Steps, common.Load.Steps...)
					receipts[i].Load.Steps[0].Name = "rare"
					receipts[i].Load.Steps[1].Name = "common"
				}
			}
			before, after := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
			for _, path := range []string{before, after} {
				if err := main.WriteTestReport(path, receipts); err != nil {
					t.Fatal(err)
				}
			}
			budget := filepath.Join(dir, "budget.yaml")
			main.WriteSuiteTestFile(t, budget, "version: 1\n"+tc.budget+"\n")
			code, problem := main.RunWithStderr(t, func() int { return main.CmdTest([]string{"compare", before, after, "--budget", budget}) })
			var comparison main.TestReportComparison
			if err := json.Unmarshal(stdout.Bytes(), &comparison); err != nil {
				t.Fatalf("comparison exit %d: %s; %v", code, problem, err)
			}
			if (code == 0) != tc.pass || comparison.Gate == nil || (comparison.Gate.Status == "passed") != tc.pass {
				t.Fatalf("evidence gate exit %d: %+v; want pass=%t", code, comparison.Gate, tc.pass)
			}
			if !tc.pass && comparison.Gate.Inconclusive == 0 {
				t.Fatalf("insufficient evidence was not reported as inconclusive: %+v", comparison.Gate)
			}
		})
	}
}

func TestScenarioReportDestinationsCheckedBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	main.CaptureSuiteTestOutput(t)
	scenario := main.TestScenario{Project: "audit-app", Command: []string{"true"}, Simulation: []string{"sh", "-c", "printf ran > started"}}
	manifest := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"audit": scenario}})
	code, problem := main.RunWithStderr(t, func() int {
		return main.CmdTest([]string{"--manifest", manifest, "--scenario", "audit", "--engine", "simulated", "--report", filepath.Join(dir, "missing", "report.json")})
	})
	if code == 0 || !strings.Contains(problem, "report output") {
		t.Errorf("invalid output exit %d: %s", code, problem)
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); !os.IsNotExist(err) {
		t.Fatal("scenario ran before rejecting the unavailable report directory")
	}
}

func TestScenarioLateReportFailurePreservesRemainingEvidence(t *testing.T) {
	for _, failing := range []string{"report", "junit", "html"} {
		t.Run(failing, func(t *testing.T) {
			dir := t.TempDir()
			stdout, _ := main.CaptureSuiteTestOutput(t)
			if err := os.Mkdir(filepath.Join(dir, "vanishing"), 0o700); err != nil {
				t.Fatal(err)
			}
			scenario := main.TestScenario{Project: "audit-app", Command: []string{"true"}, Simulation: []string{"sh", "-c", "rmdir vanishing"}}
			manifest := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"audit": scenario}})
			paths := map[string]string{"report": filepath.Join(dir, "results.json"), "junit": filepath.Join(dir, "results.xml"), "html": filepath.Join(dir, "results.html")}
			paths[failing] = filepath.Join(dir, "vanishing", "unwritable")
			code, problem := main.RunWithStderr(t, func() int {
				return main.CmdTest([]string{"--manifest", manifest, "--scenario", "audit", "--engine", "simulated", "--report", paths["report"], "--junit", paths["junit"], "--html", paths["html"]})
			})
			if code == 0 || !strings.Contains(problem, "Could not save") {
				t.Errorf("late output failure exit %d: %s", code, problem)
			}
			var receipts []main.TestRunReceipt
			if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 1 || receipts[0].Status != "passed" {
				t.Errorf("JSON stdout lost execution evidence: %s (%v)", stdout, err)
			}
			for kind, path := range paths {
				if kind == failing {
					continue
				}
				if info, err := os.Stat(path); err != nil || info.Size() == 0 {
					t.Errorf("remaining %s report missing: %v", kind, err)
				}
			}
		})
	}
}

func TestScenarioMultipleReportFailuresStillPrintReceipts(t *testing.T) {
	dir := t.TempDir()
	stdout, _ := main.CaptureSuiteTestOutput(t)
	code, problem := main.RunWithStderr(t, func() int {
		return main.FinishTestRunReports("", []main.TestRunReceipt{{Scenario: "audit", Status: "failed", Error: "original assertion failure"}},
			filepath.Join(dir, "missing", "a.json"), filepath.Join(dir, "missing", "a.xml"), filepath.Join(dir, "missing", "a.html"))
	})
	if code == 0 || !strings.Contains(problem, "JSON report") || !strings.Contains(problem, "JUnit report") || !strings.Contains(problem, "HTML report") {
		t.Errorf("report errors were not collected: exit %d: %s", code, problem)
	}
	var receipts []main.TestRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 1 || receipts[0].Error != "original assertion failure" {
		t.Fatalf("failed receipts were lost: %s (%v)", stdout, err)
	}
}

func TestScenarioComparisonOutputFailuresPreserveRemainingEvidence(t *testing.T) {
	for _, failing := range []string{"html", "markdown", "summary", "all"} {
		for _, jsonOutput := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/json=%t", failing, jsonOutput), func(t *testing.T) {
				dir := t.TempDir()
				stdout, _ := main.CaptureSuiteTestOutput(t)
				*main.JsonOutput = jsonOutput
				before, after := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
				for path, status := range map[string]string{before: "passed", after: "failed"} {
					if err := main.WriteTestReport(path, []main.TestRunReceipt{main.ComparisonBudgetReceipt(10, 15, 0, 100, 100, 10, status)}); err != nil {
						t.Fatal(err)
					}
				}
				budget := filepath.Join(dir, "budget.yaml")
				main.WriteSuiteTestFile(t, budget, "version: 1\nruns: {require_passing: true}\n")
				paths := map[string]string{"html": filepath.Join(dir, "result.html"), "markdown": filepath.Join(dir, "result.md"), "summary": filepath.Join(dir, "summary.md")}
				for kind := range paths {
					if kind == failing || failing == "all" {
						paths[kind] = filepath.Join(dir, "missing", kind)
					}
				}
				if failing != "summary" && failing != "all" {
					// GitHub Actions creates this file before invoking the CLI.
					main.WriteSuiteTestFile(t, paths["summary"], "")
				}
				t.Setenv("GITHUB_STEP_SUMMARY", paths["summary"])
				code, problem := main.RunWithStderr(t, func() int {
					return main.CmdTest([]string{"compare", before, after, "--budget", budget, "--html", paths["html"], "--markdown", paths["markdown"], "--github-summary"})
				})
				if code == 0 {
					t.Fatal("report write failure returned success")
				}
				if jsonOutput {
					var comparison main.TestReportComparison
					if err := json.Unmarshal(stdout.Bytes(), &comparison); err != nil || comparison.Summary.Matched != 1 || comparison.Gate == nil || comparison.Gate.Status != "failed" {
						t.Errorf("JSON stdout lost comparison and gate evidence: %s (%v)", stdout, err)
					}
				} else if !strings.Contains(stdout.String(), "Regression budget gate: FAILED") {
					t.Errorf("text stdout lost comparison evidence: %s", stdout)
				}
				for kind, path := range paths {
					if kind == failing || failing == "all" {
						if !strings.Contains(problem, path) {
							t.Errorf("output error omitted %s failure: %s", kind, problem)
						}
						continue
					}
					if info, err := os.Stat(path); err != nil || info.Size() == 0 {
						t.Errorf("remaining %s report missing: %v", kind, err)
					}
				}
			})
		}
	}
}

func TestScenarioRehearsalOutputFailuresPreserveRemainingEvidence(t *testing.T) {
	for _, failing := range []string{"json", "html", "md", "json html md"} {
		t.Run(failing, func(t *testing.T) {
			stdout, _ := main.CaptureSuiteTestOutput(t)
			o, _ := main.RehearsalTestRepo(t)
			outputs := t.TempDir()
			t.Setenv("REHEARSAL_TEST_OUTPUT_DIR", outputs)
			t.Setenv("REHEARSAL_TEST_BROKEN_OUTPUTS", failing)
			observer := filepath.Join(o.Repo, "checks", "observe.sh")
			body, err := os.ReadFile(observer)
			if err != nil {
				t.Fatal(err)
			}
			// Make a reserved output unwritable after validation, during execution.
			main.WriteSuiteTestFile(t, observer, `for suffix in $REHEARSAL_TEST_BROKEN_OUTPUTS; do
  path="$REHEARSAL_TEST_OUTPUT_DIR/result.$suffix"
  if [ -f "$path" ]; then
    rm "$path"
    mkdir "$path"
  fi
done
`+string(body))
			paths := map[string]string{"json": filepath.Join(outputs, "result.json"), "html": filepath.Join(outputs, "result.html"), "md": filepath.Join(outputs, "result.md")}
			code, problem := main.RunWithStderr(t, func() int {
				return main.CmdTest([]string{"rehearse", "--repo", o.Repo, "--manifest", o.Manifest, "--scenario", o.Scenario,
					"--base", o.Base, "--candidate", o.Base, "--engine", "simulated",
					"--report", paths["json"], "--html", paths["html"], "--markdown", paths["md"]})
			})
			if code == 0 {
				t.Fatal("late rehearsal output failure returned success")
			}
			var report struct {
				Status        string                `json:"status"`
				BaselineRuns  []main.TestRunReceipt `json:"baseline_runs"`
				CandidateRuns []main.TestRunReceipt `json:"candidate_runs"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Status != "passed" || len(report.BaselineRuns) != 1 || len(report.CandidateRuns) != 1 {
				t.Errorf("JSON stdout lost successful rehearsal evidence: %s (%v)", stdout, err)
			}
			for kind, path := range paths {
				if strings.Contains(failing, kind) {
					if !strings.Contains(problem, path) {
						t.Errorf("output error omitted %s failure: %s", kind, problem)
					}
					continue
				}
				if info, err := os.Stat(path); err != nil || info.Size() == 0 {
					t.Errorf("remaining %s report missing: %v", kind, err)
				}
			}
		})
	}
}

func runLoadEvidenceCommandHelper(t *testing.T, stage string) {
	t.Helper()
	if os.Getenv("LOAD_EVIDENCE_HELPER") != "1" {
		return
	}
	path := os.Getenv("GREGALE_TEST_LOAD_FILE")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("load evidence is not private: %v", err)
	}
	if os.Getenv("LOAD_EVIDENCE_LARGE") == "1" {
		if len(body) < 128<<10 || os.Getenv("GREGALE_TEST_LOAD_JSON") != "" {
			t.Fatal("large evidence must be complete in the file and absent from the inline variable")
		}
	} else if string(body) != os.Getenv("GREGALE_TEST_LOAD_JSON") {
		t.Fatal("small inline evidence differs from the complete evidence file")
	}
	var evidence main.TestLoadEvidence
	if err := json.Unmarshal(body, &evidence); err != nil || evidence.Requests == 0 {
		t.Fatalf("load evidence could not be read: %v", err)
	}
	file, err := os.OpenFile(os.Getenv("LOAD_EVIDENCE_TRACE"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := fmt.Fprintln(file, stage+"\t"+path); err != nil {
		t.Fatal(err)
	}
}

func TestScenarioLoadEvidenceAssertionHelper(t *testing.T) {
	runLoadEvidenceCommandHelper(t, "assertion")
}
func TestScenarioLoadEvidenceCleanupHelper(t *testing.T) { runLoadEvidenceCommandHelper(t, "cleanup") }

func TestScenarioLoadEvidenceFileSurvivesThroughCleanup(t *testing.T) {
	for _, mode := range []string{"small", "large", "failed"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			main.CaptureSuiteTestOutput(t)
			trace := filepath.Join(dir, "trace")
			t.Setenv("LOAD_EVIDENCE_HELPER", "1")
			t.Setenv("LOAD_EVIDENCE_TRACE", trace)
			t.Setenv("LOAD_EVIDENCE_LARGE", "0")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if mode == "failed" {
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			scenario := main.TestScenario{Load: &main.TestLoadSpec{VUs: 1, Iterations: 1},
				Command: []string{os.Args[0], "-test.run=^TestScenarioLoadEvidenceAssertionHelper$"},
				Cleanup: [][]string{{os.Args[0], "-test.run=^TestScenarioLoadEvidenceCleanupHelper$"}},
			}
			steps := 1
			if mode == "large" {
				steps = 100
				t.Setenv("LOAD_EVIDENCE_LARGE", "1")
			}
			for i := 0; i < steps; i++ {
				path := "/health"
				if mode == "large" {
					path += strings.Repeat("x", 1800)
				}
				scenario.Requests = append(scenario.Requests, main.TestHTTPRequest{Name: fmt.Sprintf("read-%d", i), Method: "GET", Path: path, Expect: main.TestHTTPExpect{Status: 200}})
			}
			cfg, err := main.ResolveTestLoadConfig(scenario, main.TestLoadOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			receipt := main.RunLocalTestWithLoad(t.Context(), "audit", scenario, dir, server.URL, main.TestDataCase{}, cfg)
			if (receipt.Status == "passed") != (mode != "failed") || receipt.CleanupError != "" {
				t.Fatalf("load evidence command execution = %s, %s, %s", receipt.Status, receipt.Error, receipt.CleanupError)
			}
			body, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			want := 2
			if mode == "failed" {
				want = 1
			}
			if len(lines) != want || !strings.HasPrefix(lines[len(lines)-1], "cleanup\t") {
				t.Fatalf("assertion/cleanup evidence trace = %s", body)
			}
			for _, line := range lines {
				_, path, _ := strings.Cut(line, "\t")
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("load evidence file leaked: %s (%v)", path, err)
				}
			}
		})
	}
}
