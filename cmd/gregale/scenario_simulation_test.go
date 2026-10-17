// adr: 1350
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func testSimulatedActionSnapshot(runID string, count int) api.ScenarioActionSnapshot {
	s := testSealedActionSnapshot(runID, 0)
	s.Version, s.Scope, s.SimulationVersion, s.SimulationSHA256 = 2, api.ScenarioSimulationScope, 1, strings.Repeat("d", 64)
	s.RecorderVersion, s.RecorderSessions = 1, 1
	for i := range count {
		s.SimulatedAttempts = append(s.SimulatedAttempts, api.ScenarioSimulationAttempt{ID: uuid.NewString(), Workload: "primary", Fixture: "notification", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", RequestSHA256: strings.Repeat("e", 64), IdempotencySHA256: strings.Repeat("f", 64), Payload: json.RawMessage(`{"subject":"<script>unsafe</script>"}`), Attempt: i + 1, Status: "completed", ResponseStatus: 200})
	}
	return s
}

func TestRehearsalSimulationCollectorAndReportsPreserveAttemptScope(t *testing.T) {
	_, _ = captureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	baseline := runSimulatedTest(t.Context(), "import", scenario, dir)
	baseline.Engine, baseline.Attempt, baseline.RunID = "real-vm", 1, strings.Repeat("a", 32)
	attach := func(count int) testRunReceipt {
		r := baseline
		r.Actions = &testActionEvidence{Network: "contained", Webhooks: "capture", PolicyVersion: 1, Status: "applied", SimulationSHA256: strings.Repeat("d", 64)}
		collect := newTestActionCollector(t.Context(), scenarioActionClientFunc(func(context.Context, string) (api.ScenarioActionSnapshot, error) {
			return testSimulatedActionSnapshot(r.RunID, count), nil
		}), &r, []string{"primary"})
		collect()
		return r
	}
	before, after := attach(1), attach(2)
	comparison, err := compareTestReports("before", "after", []testRunReceipt{before}, []testRunReceipt{after})
	if err != nil || comparison.ActionStatus != "failed" || comparison.Runs[0].Actions.Scope != api.ScenarioSimulationScope {
		t.Fatalf("simulation comparison: %+v %v", comparison, err)
	}
	out := t.TempDir()
	html, markdown := filepath.Join(out, "comparison.html"), filepath.Join(out, "comparison.md")
	if err := writeTestComparisonHTML(html, comparison); err != nil {
		t.Fatal(err)
	}
	if err := writeTestComparisonMarkdown(markdown, comparison); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{html, markdown} {
		body, err := os.ReadFile(name)
		if err != nil || !strings.Contains(string(body), "notification") || !strings.Contains(string(body), "simulated attempts") || strings.Contains(string(body), "<script>unsafe</script>") {
			t.Fatalf("unsafe or missing simulation report %s: %v", name, err)
		}
	}
	before.Actions.SimulationSHA256 = strings.Repeat("c", 64)
	if c := compareTestActions(before, after); c.Status != "inconclusive" {
		t.Fatal("changed fixture identity compared as equivalent")
	}
}
