package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

const behaviorTestDocument = `{"version":1,"observations":[{"key":"notification.successes","value":1}]}`

func behaviorTestScenario(t *testing.T) (string, main.TestScenario) {
	t.Helper()
	dir := t.TempDir()
	main.WriteSuiteTestFile(t, filepath.Join(dir, "fixture.json"), `{"report":"private-fixture-input"}`)
	main.WriteSuiteTestFile(t, filepath.Join(dir, "observe.sh"), `printf '%s' '`+behaviorTestDocument+`' > "$GREGALE_TEST_BEHAVIOR_OUTPUT"
printf '%s' "$GREGALE_TEST_BEHAVIOR_OUTPUT" > output-path.txt
exit "${BEHAVIOR_TEST_EXIT:-0}"
`)
	return dir, main.TestScenario{Project: "behavior-test", Source: ".", Command: []string{"sh", "observe.sh"}, Simulation: []string{"sh", "observe.sh"}, Timeout: "5s",
		Behavior: &main.TestBehaviorSpec{Version: 1, Fixtures: []string{"fixture.json"}, Observers: []string{"observe.sh"},
			Observations: []behaviorcompare.Spec{{Key: "notification.successes", Kind: "delivery"}}}}
}

func TestBehaviorCollectionAcrossLocalAndSimulatedRunners(t *testing.T) {
	main.CaptureSuiteTestOutput(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	for _, engine := range []string{"local", "simulated"} {
		for _, exit := range []string{"0", "1"} {
			t.Run(engine+"/exit-"+exit, func(t *testing.T) {
				t.Setenv("BEHAVIOR_TEST_EXIT", exit)
				dir, scenario := behaviorTestScenario(t)
				var receipt main.TestRunReceipt
				if engine == "local" {
					receipt = main.RunLocalTest(context.Background(), "delivery-smoke", scenario, dir, server.URL, main.TestDataCase{})
				} else {
					receipt = main.RunSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
				}
				if receipt.Behavior == nil || receipt.Behavior.Status != "complete" || string(receipt.Behavior.Observations[0].Value) != "1" {
					t.Fatalf("behavior collection failed: %+v", receipt)
				}
				if (receipt.Status == "passed") != (exit == "0") {
					t.Fatalf("command outcome was lost: %+v", receipt)
				}
				output, err := os.ReadFile(filepath.Join(dir, "output-path.txt"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(string(output)); !os.IsNotExist(err) {
					t.Fatalf("private behavior file was retained: %v", err)
				}
				encoded, _ := json.Marshal(receipt)
				if bytes.Contains(encoded, []byte("private-fixture-input")) || bytes.Contains(encoded, output) {
					t.Fatal("receipt leaked fixture contents or temporary output path")
				}
			})
		}
	}
}

func TestBehaviorCollectionFailsClosedAndCleansPrivateFiles(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "fixture-changed", "observer-changed", "symlink", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			dir, scenario := behaviorTestScenario(t)
			receipt := main.TestRunReceipt{Status: "passed"}
			session, err := main.PrepareTestBehavior(dir, scenario, &receipt)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "malformed":
				main.WriteSuiteTestFile(t, (*session.OutputPath()), `{"private":"do-not-echo"}`)
			case "fixture-changed", "observer-changed":
				file := "fixture.json"
				if mode == "observer-changed" {
					file = "observe.sh"
				}
				main.WriteSuiteTestFile(t, filepath.Join(dir, file), "changed")
				main.WriteSuiteTestFile(t, (*session.OutputPath()), behaviorTestDocument)
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "fixture.json"), (*session.OutputPath())); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				main.WriteSuiteTestFile(t, (*session.OutputPath()), strings.Repeat(" ", behaviorcompare.MaxDocumentBytes+1))
			}
			session.Finish(&receipt)
			session.Finish(&receipt)
			if receipt.Status != "failed" || receipt.Behavior.Status != "incomplete" || strings.Contains(receipt.Error, "do-not-echo") {
				t.Fatalf("invalid observation handling: %+v", receipt)
			}
			if _, err := os.Stat((*session.Dir())); !os.IsNotExist(err) {
				t.Fatalf("behavior directory leaked: %v", err)
			}
		})
	}
}

func TestBehaviorCollectionPrecedesLocalFixtureCleanup(t *testing.T) {
	main.CaptureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	scenario.Cleanup = [][]string{{"sh", "-c", `printf 'retired' > fixture.json`}}
	receipt := main.RunLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", main.TestDataCase{})
	if receipt.Status != "passed" || receipt.Behavior == nil || receipt.Behavior.Status != "complete" {
		t.Fatalf("fixture cleanup invalidated already collected evidence: %+v", receipt)
	}
	body, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	if err != nil || string(body) != "retired" {
		t.Fatalf("fixture cleanup did not execute: %s (%v)", body, err)
	}
}

func TestBehaviorOptInSurvivesEarlyRunnerFailures(t *testing.T) {
	main.CaptureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	scenario.Source = "missing-source"
	client := main.NewClient("http://127.0.0.1:1", "unused-test-key")
	for _, receipt := range []main.TestRunReceipt{
		main.RunLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", main.TestDataCase{}),
		main.RunSimulatedTest(context.Background(), "delivery-smoke", scenario, dir),
		main.RunTestProfile(context.Background(), client, "delivery-smoke", scenario, dir, "warm"),
	} {
		if receipt.Status != "failed" || receipt.Behavior == nil || receipt.Behavior.Status != "incomplete" {
			t.Fatalf("early failure lost behavior opt-in: %+v", receipt)
		}
		if main.CompareTestBehavior(receipt, receipt).Status != "inconclusive" {
			t.Fatal("two early failures were reported as equivalent")
		}
	}
}

func TestBehaviorComparisonRejectsChangedRecipeAndCaseValues(t *testing.T) {
	main.CaptureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	before := main.RunSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
	scenario.Simulation = []string{"sh", "observe.sh", "changed-journey"}
	after := main.RunSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
	if before.Status != "passed" || after.Status != "passed" || main.CompareTestBehavior(before, after).Status != "inconclusive" {
		t.Fatal("changed command recipe was accepted as equivalent")
	}
	first := main.TestDataCase{Name: "row-1", Values: map[string]any{"customer": "first"}}
	second := main.TestDataCase{Name: "row-1", Values: map[string]any{"customer": "second"}}
	before = main.RunLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", first)
	after = main.RunLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", second)
	if before.Status != "passed" || after.Status != "passed" || main.CompareTestBehavior(before, after).Status != "inconclusive" {
		t.Fatal("different case values were accepted as equivalent")
	}
}

func TestBehaviorManifestValidationAndSourceBoundary(t *testing.T) {
	for _, mode := range []string{"version", "missing-fixtures", "missing-observers", "duplicate-key", "traversal", "outside-symlink", "missing-file", "resilience"} {
		t.Run(mode, func(t *testing.T) {
			dir, scenario := behaviorTestScenario(t)
			switch mode {
			case "version":
				scenario.Behavior.Version = 2
			case "missing-fixtures":
				scenario.Behavior.Fixtures = nil
			case "missing-observers":
				scenario.Behavior.Observers = nil
			case "duplicate-key":
				scenario.Behavior.Observations = append(scenario.Behavior.Observations, scenario.Behavior.Observations[0])
			case "traversal":
				scenario.Behavior.Fixtures = []string{"../fixture.json"}
			case "outside-symlink":
				outside := filepath.Join(t.TempDir(), "outside.json")
				main.WriteSuiteTestFile(t, outside, "private-outside-value")
				if err := os.Symlink(outside, filepath.Join(dir, "outside.json")); err != nil {
					t.Fatal(err)
				}
				scenario.Behavior.Fixtures = []string{"outside.json"}
			case "missing-file":
				scenario.Behavior.Fixtures = []string{"missing.json"}
			case "resilience":
				scenario.Resilience = &main.TestResilienceSpec{}
			}
			manifest := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"delivery-smoke": scenario}})
			if _, _, err := main.ReadTestManifest(manifest); err == nil {
				t.Fatal("invalid behavior manifest accepted")
			}
		})
	}
}

func TestBehaviorExportExampleDetectsDuplicateNotification(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for the export acceptance fixture")
	}
	stdout, stderr := main.CaptureSuiteTestOutput(t)
	manifest := filepath.Join("..", "..", "tests", "scenario-acceptance", "behavior", "gregale-test.yaml")
	scenarios, dir, err := main.ReadTestManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	reports := make([]main.TestRunReceipt, 0, 2)
	for _, variant := range []string{"baseline", "duplicate"} {
		t.Setenv("GREGALE_BEHAVIOR_EXAMPLE_VARIANT", variant)
		receipt := main.RunSimulatedTest(context.Background(), "customer-export", scenarios["customer-export"], dir)
		receipt.Attempt = 1
		if receipt.Status != "passed" || receipt.Behavior == nil || receipt.Behavior.Status != "complete" {
			t.Fatalf("%s execution failed: %+v\n%s", variant, receipt, stderr)
		}
		reports = append(reports, receipt)
	}
	comparison, err := main.CompareTestReports("base", "candidate", reports[:1], reports[1:])
	if err != nil || comparison.BehaviorStatus != "failed" {
		t.Fatalf("comparison = %+v (%v)", comparison, err)
	}
	changes := comparison.Runs[0].Behavior.Changes
	if len(changes) != 3 || changes[0].Status != "unchanged" || changes[1].Status != "unchanged" || changes[2].Key != "notification.successful_deliveries" || string(changes[2].Before) != "1" || string(changes[2].After) != "2" {
		t.Fatalf("unexpected behavior differences: %+v", changes)
	}
	output := t.TempDir()
	before, after := filepath.Join(output, "before.json"), filepath.Join(output, "after.json")
	if err := main.WriteTestReport(before, reports[:1]); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteTestReport(after, reports[1:]); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	html, markdown := filepath.Join(output, "comparison.html"), filepath.Join(output, "comparison.md")
	if code := main.CmdTestCompare([]string{before, after, "--html", html, "--markdown", markdown}); code != 1 {
		t.Fatalf("behavior change must fail without a performance budget: exit %d; %s", code, stderr)
	}
	for _, report := range []string{html, markdown} {
		body, err := os.ReadFile(report)
		key := "notification.successful_deliveries"
		if report == markdown {
			key = main.MarkdownComparisonText(key)
		}
		if err != nil || !bytes.Contains(body, []byte(key)) || !bytes.Contains(body, []byte("unchanged")) || !bytes.Contains(body, []byte("changed")) {
			t.Fatalf("missing behavior comparison in %s: %v", report, err)
		}
	}
}

func TestBehaviorComparisonFailsClosedForMissingAndFailedRuns(t *testing.T) {
	stdout, stderr := main.CaptureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	good := main.RunSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
	good.Attempt = 1
	if good.Status != "passed" {
		t.Fatalf("baseline: %+v", good)
	}
	for _, mode := range []string{"unchanged", "missing-evidence", "failed", "cleanup", "missing-run", "fixture", "observer", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			before, after := good, good
			if mode == "legacy" {
				before.Behavior, after.Behavior = nil, nil
			} else if mode == "missing-evidence" {
				after.Behavior = nil
			} else if mode == "failed" {
				after.Status = "failed"
			} else if mode == "cleanup" {
				after.CleanupError = "cleanup failed"
			} else if mode == "missing-run" {
				after.Scenario = "different-scenario"
			} else if mode == "fixture" || mode == "observer" {
				copy := *good.Behavior
				after.Behavior = &copy
				if mode == "fixture" {
					copy.FixtureSHA256 = strings.Repeat("c", 64)
				} else {
					copy.ContractSHA256 = strings.Repeat("d", 64)
				}
			}
			beforePath, afterPath := filepath.Join(t.TempDir(), "before.json"), filepath.Join(t.TempDir(), "after.json")
			if err := main.WriteTestReport(beforePath, []main.TestRunReceipt{before}); err != nil {
				t.Fatal(err)
			}
			if err := main.WriteTestReport(afterPath, []main.TestRunReceipt{after}); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "unchanged" || mode == "legacy" {
				want = 0
			}
			stdout.Reset()
			if code := main.CmdTestCompare([]string{beforePath, afterPath}); code != want {
				t.Fatalf("comparison exit %d, want %d: %s", code, want, stderr)
			}
		})
	}
}
