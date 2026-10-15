package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func testPhaseComparisonFixture(name, role string, requests, failures int, durationMS int64, p95 float64) testLoadChaosPhaseEvidence {
	return testLoadChaosPhaseEvidence{
		Name: name, Role: role, ActualDurationMS: durationMS,
		testLoadMetrics: testLoadMetrics{
			Requests: requests, Failures: failures,
			Latency: testLoadLatency{Samples: requests, P95MS: p95},
		},
	}
}

func TestPhaseComparisonsRetainOriginalBaselineAcrossOutages(t *testing.T) {
	// adr: 984 — campaign evidence must not hide degradation behind a later baseline.
	report := &testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{
		testPhaseComparisonFixture("original", "baseline", 20, 1, 2000, 100),
		testPhaseComparisonFixture("outage-one", "fault", 10, 5, 2000, 200),
		testPhaseComparisonFixture("recovered-one", "recovery", 20, 2, 4000, 120),
		testPhaseComparisonFixture("outage-two", "fault", 20, 5, 2000, 150),
		testPhaseComparisonFixture("recovered-two", "recovery", 20, 1, 2000, 125),
	}}
	rows := testLoadPhaseComparisons(report)
	if len(rows) != 5 {
		t.Fatalf("got %d phase rows", len(rows))
	}
	last := rows[4]
	if last.P95ChangePercent == nil || math.Abs(*last.P95ChangePercent-25) > 1e-9 {
		t.Fatalf("last phase did not use original p95: %+v", last)
	}
	fault := rows[1]
	if fault.RequestsPerSecond == nil || *fault.RequestsPerSecond != 5 ||
		fault.RequestRateChangePercent == nil || *fault.RequestRateChangePercent != -50 ||
		fault.ErrorRateChangePoints == nil || *fault.ErrorRateChangePoints != 45 {
		t.Fatalf("rates must use actual duration and percentage points: %+v", fault)
	}
	if rows[0].P95ChangePercent != nil || report.ChaosPhases[0].Latency.P95MS != 100 {
		t.Fatal("comparison changed the baseline or invented its delta")
	}
}

func TestPhaseComparisonsKeepUnavailableEvidenceDistinctFromZero(t *testing.T) {
	// adr: 984 — missing evidence cannot become successful descriptive measurements.
	for _, tc := range []struct {
		name     string
		baseline testLoadChaosPhaseEvidence
		current  testLoadChaosPhaseEvidence
	}{
		{"empty", testPhaseComparisonFixture("base", "baseline", 0, 0, 0, 0), testLoadChaosPhaseEvidence{}},
		{"zero latency", testPhaseComparisonFixture("base", "baseline", 10, 0, 1000, 0), testPhaseComparisonFixture("current", "recovery", 10, 0, 1000, 2)},
		{"nonfinite latency", testPhaseComparisonFixture("base", "baseline", 10, 0, 1000, math.NaN()), testPhaseComparisonFixture("current", "recovery", 10, 0, 1000, math.Inf(1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := testLoadPhaseComparisons(&testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{tc.baseline, tc.current}})
			if rows[1].P95ChangePercent != nil {
				t.Fatalf("unavailable percentage became %v", *rows[1].P95ChangePercent)
			}
		})
	}
	if rows := testLoadPhaseComparisons(nil); rows != nil {
		t.Fatal("nil load produced a comparison")
	}
	phase := testPhaseComparisonFixture("cancelled", "recovery", 0, 0, 1000, 0)
	row := testLoadPhaseComparisons(&testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{phase}})[0]
	if row.P95MS != nil || row.ErrorRatePercent != nil || row.RequestsPerSecond == nil || *row.RequestsPerSecond != 0 {
		t.Fatalf("zero request rate must not imply measured zero latency or errors: %+v", row)
	}
	phase = testPhaseComparisonFixture("missing latency samples", "recovery", 3, 0, 1000, 50)
	phase.Latency.Samples = 0
	if testLoadPhaseComparisons(&testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{phase}})[0].P95MS != nil {
		t.Fatal("missing latency samples produced a p95")
	}
}

func TestPhaseComparisonsUseRecordedRolesAndKeepStrictRetryFailures(t *testing.T) {
	// adr: 984 — phase names and a good average do not prove exercised fault behavior.
	phases := []testLoadChaosPhase{
		{Name: "fault-in-name-only"},
		{Name: "normal-in-name-only", Rules: []testChaosRule{{To: "dependency"}}},
		{Name: "clear"},
	}
	report := &testLoadEvidence{}
	for i, phase := range phases {
		report.ChaosPhases = append(report.ChaosPhases, testPhaseComparisonFixture(phase.Name, testLoadPhaseRole(i, phases), 10, 0, 1000, 10))
	}
	strict := &testPerRequestAttemptEvidence{Requests: 10, Correlated: 10, MaxAttempts: 11, Limit: 2, ViolationCount: 1, Error: "one retry outlier"}
	report.ChaosPhases[1].RetryAmplification = &testRetryAmplificationEvidence{
		Passed: true, Requests: 10, Attempts: 20, AttemptsPerRequest: 2, MaxAttemptsPerRequest: 2, PerRequest: strict,
	}
	report.ChaosPhases[2].StepRecovery = map[string]*testLoadRecoveryEvidence{"missing": nil}
	rows := testLoadPhaseComparisons(report)
	if rows[0].Role != "baseline" || rows[1].Role != "fault" || rows[2].Role != "recovery" {
		t.Fatalf("roles were inferred from names: %+v", rows)
	}
	if !strings.Contains(strings.Join(rows[1].Findings, "; "), "one retry outlier") ||
		!strings.Contains(strings.Join(rows[1].Attempts, "; "), "per-request maximum 11") {
		t.Fatalf("good average hid a strict failure: %+v", rows[1])
	}
	if !strings.Contains(strings.Join(rows[2].Findings, "; "), "evidence unavailable") {
		t.Fatal("missing named recovery evidence disappeared")
	}
	// Historical reports without explicit roles still use recorded evidence.
	report.ChaosPhases[1].Role = ""
	report.ChaosPhases[1].Chaos = &testChaosEvidence{Rules: []chaos.Rule{{Kind: chaos.KindTCPLatency}}}
	if testLoadPhaseComparisons(report)[1].Role != "fault" {
		t.Fatal("historical rule evidence was ignored")
	}
}

func TestPhaseComparisonReportsEscapeDataAndPreserveJUnitVerdict(t *testing.T) {
	// adr: 984 — report views preserve assertions, raw evidence and scenario verdicts.
	load := &testLoadEvidence{Status: "failed", ChaosPhases: []testLoadChaosPhaseEvidence{
		testPhaseComparisonFixture("base", "baseline", 10, 0, 1000, 100),
		testPhaseComparisonFixture("<script>fault</script>", "fault", 10, 5, 1000, 200),
	}}
	load.ChaosPhases[1].Thresholds = []testLoadThresholdEvidence{{Metric: "p95_ms", Operator: "<=", Limit: 150, Actual: 200}}
	receipt := testRunReceipt{
		Scenario: "comparison", Status: "failed", Error: "p95 assertion failed", Load: load,
		Steps: []testScenarioStepEvidence{{Name: "staged-load", Status: "failed", Load: load}},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if strings.Count(html, "<h3>Baseline / fault / recovery comparison</h3>") != 2 {
		t.Fatal("comparison missing from top-level or staged load")
	}
	for _, text := range []string{"&lt;script&gt;fault&lt;/script&gt;", "100.000 %", "50.000 pp", "p95_ms &lt;= 150, observed 200"} {
		if !strings.Contains(html, text) {
			t.Fatalf("HTML omitted %q", text)
		}
	}
	if strings.Contains(html, "<script>fault</script>") {
		t.Fatal("phase name was not escaped")
	}
	var terminal bytes.Buffer
	printTestLoadPhaseComparisons(&terminal, load)
	if !strings.Contains(terminal.String(), "p95_ms <= 150, observed 200") {
		t.Fatal("terminal omitted the failed assertion")
	}
	junit := filepath.Join(dir, "report.xml")
	if err := writeTestJUnit(junit, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Failures != 1 || len(suite.Cases) != 1 || suite.Cases[0].Failure == nil {
		t.Fatal("informational comparison changed the JUnit verdict")
	}
	var retained testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &retained); err != nil {
		t.Fatal(err)
	}
	if retained.Load.ChaosPhases[1].Role != "fault" || retained.Load.ChaosPhases[1].Thresholds[0].Actual != 200 {
		t.Fatal("JUnit lost the phase role or failed assertion measurements")
	}
}
