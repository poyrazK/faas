package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
)

type scenarioActionClientFunc func(context.Context, string) (api.ScenarioActionSnapshot, error)

func (f scenarioActionClientFunc) SealScenarioTestActions(ctx context.Context, runID string) (api.ScenarioActionSnapshot, error) {
	return f(ctx, runID)
}

func testSealedActionSnapshot(runID string, count int) api.ScenarioActionSnapshot {
	snapshot := api.ScenarioActionSnapshot{Version: api.ScenarioActionSnapshotVersion, Scope: api.ScenarioActionScope, RunID: runID,
		Status: "complete", SealedAt: "2026-10-16T21:30:00Z", Workloads: []string{"primary"}, Intents: []api.ScenarioCapturedAction{}, CapturedCount: int64(count)}
	for range count {
		snapshot.Intents = append(snapshot.Intents, api.ScenarioCapturedAction{ID: uuid.NewString(), OperationID: uuid.NewString(), WebhookID: uuid.NewString(),
			Workload: "primary", Source: "operation", Name: "notify", Type: "import.completed", ReceiverSHA256: strings.Repeat("c", 64), Payload: json.RawMessage(`{"order":1,"message":"<script>unsafe</script>|test"}`)})
	}
	return snapshot
}

func TestScenarioActionCollectorFailsClosedAndSurvivesCancellation(t *testing.T) {
	for _, mode := range []string{"complete", "pending-then-complete", "stale", "workload-mismatch", "truncated", "count-mismatch", "transport-error", "legacy-empty"} {
		t.Run(mode, func(t *testing.T) {
			receipt := testRunReceipt{RunID: strings.Repeat("a", 32), Status: "passed", Actions: &testActionEvidence{Webhooks: "capture", PolicyVersion: 1, Status: "applied"}}
			calls := 0
			client := scenarioActionClientFunc(func(ctx context.Context, runID string) (api.ScenarioActionSnapshot, error) {
				calls++
				if ctx.Err() != nil || runID != receipt.RunID {
					t.Fatal("capture inherited cancelled execution context or stale run identity")
				}
				snapshot := testSealedActionSnapshot(runID, 1)
				switch mode {
				case "stale":
					snapshot.RunID = strings.Repeat("b", 32)
				case "workload-mismatch":
					snapshot.Workloads = []string{"worker"}
				case "truncated":
					snapshot.Status, snapshot.Truncated, snapshot.Reason = "incomplete", true, "format bounds"
				case "count-mismatch":
					snapshot.CapturedCount++
				case "transport-error":
					return api.ScenarioActionSnapshot{}, errors.New("private transport detail")
				case "legacy-empty":
					return api.ScenarioActionSnapshot{}, nil
				case "pending-then-complete":
					if calls == 1 {
						snapshot.Status, snapshot.SealedAt, snapshot.PendingManagedWork = "pending", "", 1
					}
				}
				return snapshot, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			collect := newTestActionCollector(ctx, client, &receipt, []string{"primary"})
			collect()
			collect() // Deferred fallback must not replace already sealed evidence.
			valid := mode == "complete" || mode == "pending-then-complete"
			if (receipt.Status == "passed") != valid || (receipt.Actions.Evidence.Status == "complete") != valid || strings.Contains(receipt.Error, "private transport detail") {
				t.Fatalf("collection: %+v", receipt)
			}
			wantCalls := 1
			if mode == "pending-then-complete" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("non-idempotent collection: %d calls", calls)
			}
		})
	}
}

func TestScenarioActionComparisonGatesReportsIndependentlyOfDeclaredBehavior(t *testing.T) {
	stdout, stderr := captureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	baseline := runSimulatedTest(context.Background(), "import", scenario, dir)
	if baseline.Status != "passed" {
		t.Fatalf("baseline: %+v", baseline)
	}
	baseline.Engine, baseline.Attempt = "real-vm", 1
	baseline.RunID = strings.Repeat("a", 32)
	attach := func(run testRunReceipt, count int) testRunReceipt {
		snapshot := testSealedActionSnapshot(run.RunID, count)
		run.Actions = &testActionEvidence{Webhooks: "capture", PolicyVersion: 1, Status: "applied", Evidence: &actioncompare.Evidence{
			Version: actioncompare.Version, Status: "complete", Provenance: actioncompare.Provenance,
			ContractSHA256: run.Behavior.ContractSHA256, FixtureSHA256: run.Behavior.FixtureSHA256, Snapshot: &snapshot}}
		return run
	}
	before, after := attach(baseline, 1), attach(baseline, 2)
	comparison, err := compareTestReports("base", "candidate", []testRunReceipt{before}, []testRunReceipt{after})
	if err != nil || comparison.BehaviorStatus != "passed" || comparison.ActionStatus != "failed" || len(comparison.Runs[0].Actions.Changes) != 1 {
		t.Fatalf("duplicate notification missed: %+v %v", comparison, err)
	}
	output := t.TempDir()
	beforePath, afterPath := filepath.Join(output, "before.json"), filepath.Join(output, "after.json")
	if err := writeTestReport(beforePath, []testRunReceipt{before}); err != nil {
		t.Fatal(err)
	}
	if err := writeTestReport(afterPath, []testRunReceipt{after}); err != nil {
		t.Fatal(err)
	}
	html, markdown := filepath.Join(output, "comparison.html"), filepath.Join(output, "comparison.md")
	stdout.Reset()
	if code := cmdTestCompare([]string{beforePath, afterPath, "--html", html, "--markdown", markdown}); code != 1 {
		t.Fatalf("action regression exit=%d: %s", code, stderr)
	}
	var emitted testReportComparison
	if err := json.Unmarshal(stdout.Bytes(), &emitted); err != nil || emitted.ActionStatus != "failed" || emitted.BehaviorStatus != "passed" {
		t.Fatalf("missing action result in JSON: %+v %v", emitted, err)
	}
	stdout.Reset()
	printTestReportComparison(stdout, emitted)
	if !strings.Contains(stdout.String(), "managed webhook actions: failed (1 → 2") {
		t.Fatalf("missing action comparison: %s", stdout)
	}
	singleHTML := filepath.Join(output, "run.html")
	if err := writeTestHTMLReport(singleHTML, "import", []testRunReceipt{before}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{html, markdown, singleHTML} {
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(body, []byte("notify")) || path != singleHTML && !bytes.Contains(body, []byte("changed")) || bytes.Contains(body, []byte("<script>unsafe</script>")) {
			t.Fatalf("missing or unsafe action report %s: %v", path, err)
		}
	}
	for _, mode := range []string{"missing", "legacy-config-only", "failed-execution", "cleanup-failed"} {
		t.Run(mode, func(t *testing.T) {
			candidate := attach(baseline, 1)
			switch mode {
			case "missing":
				candidate.Actions = nil
			case "legacy-config-only":
				candidate.Actions.Evidence = nil
			case "failed-execution":
				candidate.Status = "failed"
			case "cleanup-failed":
				candidate.CleanupError = "cleanup failed"
			}
			if result := compareTestActions(before, candidate); result == nil || result.Status != "inconclusive" {
				t.Fatalf("incomplete action evidence passed: %+v", result)
			}
		})
	}
}
