package main

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

	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

const behaviorTestDocument = `{"version":1,"observations":[{"key":"notification.successes","value":1}]}`

func behaviorTestScenario(t *testing.T) (string, testScenario) {
	t.Helper()
	dir := t.TempDir()
	writeSuiteTestFile(t, filepath.Join(dir, "fixture.json"), `{"report":"private-fixture-input"}`)
	writeSuiteTestFile(t, filepath.Join(dir, "observe.sh"), `printf '%s' '`+behaviorTestDocument+`' > "$GREGALE_TEST_BEHAVIOR_OUTPUT"
printf '%s' "$GREGALE_TEST_BEHAVIOR_OUTPUT" > output-path.txt
exit "${BEHAVIOR_TEST_EXIT:-0}"
`)
	return dir, testScenario{Project: "behavior-test", Source: ".", Command: []string{"sh", "observe.sh"}, Simulation: []string{"sh", "observe.sh"}, Timeout: "5s",
		Behavior: &testBehaviorSpec{Version: 1, Fixtures: []string{"fixture.json"}, Observers: []string{"observe.sh"},
			Observations: []behaviorcompare.Spec{{Key: "notification.successes", Kind: "delivery"}}}}
}

func TestBehaviorCollectionAcrossLocalAndSimulatedRunners(t *testing.T) {
	captureSuiteTestOutput(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	for _, engine := range []string{"local", "simulated"} {
		for _, exit := range []string{"0", "1"} {
			t.Run(engine+"/exit-"+exit, func(t *testing.T) {
				t.Setenv("BEHAVIOR_TEST_EXIT", exit)
				dir, scenario := behaviorTestScenario(t)
				var receipt testRunReceipt
				if engine == "local" {
					receipt = runLocalTest(context.Background(), "delivery-smoke", scenario, dir, server.URL, testDataCase{})
				} else {
					receipt = runSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
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
			receipt := testRunReceipt{Status: "passed"}
			session, err := prepareTestBehavior(dir, scenario, &receipt)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "malformed":
				writeSuiteTestFile(t, session.outputPath, `{"private":"do-not-echo"}`)
			case "fixture-changed", "observer-changed":
				file := "fixture.json"
				if mode == "observer-changed" {
					file = "observe.sh"
				}
				writeSuiteTestFile(t, filepath.Join(dir, file), "changed")
				writeSuiteTestFile(t, session.outputPath, behaviorTestDocument)
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "fixture.json"), session.outputPath); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				writeSuiteTestFile(t, session.outputPath, strings.Repeat(" ", behaviorcompare.MaxDocumentBytes+1))
			}
			session.finish(&receipt)
			session.finish(&receipt)
			if receipt.Status != "failed" || receipt.Behavior.Status != "incomplete" || strings.Contains(receipt.Error, "do-not-echo") {
				t.Fatalf("invalid observation handling: %+v", receipt)
			}
			if _, err := os.Stat(session.dir); !os.IsNotExist(err) {
				t.Fatalf("behavior directory leaked: %v", err)
			}
		})
	}
}

func TestBehaviorCollectionPrecedesLocalFixtureCleanup(t *testing.T) {
	captureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	scenario.Cleanup = [][]string{{"sh", "-c", `printf 'retired' > fixture.json`}}
	receipt := runLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", testDataCase{})
	if receipt.Status != "passed" || receipt.Behavior == nil || receipt.Behavior.Status != "complete" {
		t.Fatalf("fixture cleanup invalidated already collected evidence: %+v", receipt)
	}
	body, err := os.ReadFile(filepath.Join(dir, "fixture.json"))
	if err != nil || string(body) != "retired" {
		t.Fatalf("fixture cleanup did not execute: %s (%v)", body, err)
	}
}

func TestBehaviorOptInSurvivesEarlyRunnerFailures(t *testing.T) {
	captureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	scenario.Source = "missing-source"
	client := NewClient("http://127.0.0.1:1", "unused-test-key")
	for _, receipt := range []testRunReceipt{
		runLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", testDataCase{}),
		runSimulatedTest(context.Background(), "delivery-smoke", scenario, dir),
		runTestProfile(context.Background(), client, "delivery-smoke", scenario, dir, "warm"),
	} {
		if receipt.Status != "failed" || receipt.Behavior == nil || receipt.Behavior.Status != "incomplete" {
			t.Fatalf("early failure lost behavior opt-in: %+v", receipt)
		}
		if compareTestBehavior(receipt, receipt).Status != "inconclusive" {
			t.Fatal("two early failures were reported as equivalent")
		}
	}
}

func TestBehaviorComparisonRejectsChangedRecipeAndCaseValues(t *testing.T) {
	captureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	before := runSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
	scenario.Simulation = []string{"sh", "observe.sh", "changed-journey"}
	after := runSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
	if before.Status != "passed" || after.Status != "passed" || compareTestBehavior(before, after).Status != "inconclusive" {
		t.Fatal("changed command recipe was accepted as equivalent")
	}
	first := testDataCase{Name: "row-1", Values: map[string]any{"customer": "first"}}
	second := testDataCase{Name: "row-1", Values: map[string]any{"customer": "second"}}
	before = runLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", first)
	after = runLocalTest(context.Background(), "delivery-smoke", scenario, dir, "http://127.0.0.1:1", second)
	if before.Status != "passed" || after.Status != "passed" || compareTestBehavior(before, after).Status != "inconclusive" {
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
				writeSuiteTestFile(t, outside, "private-outside-value")
				if err := os.Symlink(outside, filepath.Join(dir, "outside.json")); err != nil {
					t.Fatal(err)
				}
				scenario.Behavior.Fixtures = []string{"outside.json"}
			case "missing-file":
				scenario.Behavior.Fixtures = []string{"missing.json"}
			case "resilience":
				scenario.Resilience = &testResilienceSpec{}
			}
			manifest := writeSuiteTestManifest(t, dir, testManifest{Version: 1, Scenarios: map[string]testScenario{"delivery-smoke": scenario}})
			if _, _, err := readTestManifest(manifest); err == nil {
				t.Fatal("invalid behavior manifest accepted")
			}
		})
	}
}

func TestBehaviorExportExampleDetectsDuplicateNotification(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for the export acceptance fixture")
	}
	stdout, stderr := captureSuiteTestOutput(t)
	manifest := filepath.Join("..", "..", "tests", "scenario-acceptance", "behavior", "gregale-test.yaml")
	scenarios, dir, err := readTestManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	reports := make([]testRunReceipt, 0, 2)
	for _, variant := range []string{"baseline", "duplicate"} {
		t.Setenv("GREGALE_BEHAVIOR_EXAMPLE_VARIANT", variant)
		receipt := runSimulatedTest(context.Background(), "customer-export", scenarios["customer-export"], dir)
		receipt.Attempt = 1
		if receipt.Status != "passed" || receipt.Behavior == nil || receipt.Behavior.Status != "complete" {
			t.Fatalf("%s execution failed: %+v\n%s", variant, receipt, stderr)
		}
		reports = append(reports, receipt)
	}
	comparison, err := compareTestReports("base", "candidate", reports[:1], reports[1:])
	if err != nil || comparison.BehaviorStatus != "failed" {
		t.Fatalf("comparison = %+v (%v)", comparison, err)
	}
	changes := comparison.Runs[0].Behavior.Changes
	if len(changes) != 3 || changes[0].Status != "unchanged" || changes[1].Status != "unchanged" || changes[2].Key != "notification.successful_deliveries" || string(changes[2].Before) != "1" || string(changes[2].After) != "2" {
		t.Fatalf("unexpected behavior differences: %+v", changes)
	}
	output := t.TempDir()
	before, after := filepath.Join(output, "before.json"), filepath.Join(output, "after.json")
	if err := writeTestReport(before, reports[:1]); err != nil {
		t.Fatal(err)
	}
	if err := writeTestReport(after, reports[1:]); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	html, markdown := filepath.Join(output, "comparison.html"), filepath.Join(output, "comparison.md")
	if code := cmdTestCompare([]string{before, after, "--html", html, "--markdown", markdown}); code != 1 {
		t.Fatalf("behavior change must fail without a performance budget: exit %d; %s", code, stderr)
	}
	for _, report := range []string{html, markdown} {
		body, err := os.ReadFile(report)
		key := "notification.successful_deliveries"
		if report == markdown {
			key = markdownComparisonText(key)
		}
		if err != nil || !bytes.Contains(body, []byte(key)) || !bytes.Contains(body, []byte("unchanged")) || !bytes.Contains(body, []byte("changed")) {
			t.Fatalf("missing behavior comparison in %s: %v", report, err)
		}
	}
}

func TestBehaviorComparisonFailsClosedForMissingAndFailedRuns(t *testing.T) {
	stdout, stderr := captureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	good := runSimulatedTest(context.Background(), "delivery-smoke", scenario, dir)
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
			if err := writeTestReport(beforePath, []testRunReceipt{before}); err != nil {
				t.Fatal(err)
			}
			if err := writeTestReport(afterPath, []testRunReceipt{after}); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "unchanged" || mode == "legacy" {
				want = 0
			}
			stdout.Reset()
			if code := cmdTestCompare([]string{beforePath, afterPath}); code != want {
				t.Fatalf("comparison exit %d, want %d: %s", code, want, stderr)
			}
		})
	}
}
