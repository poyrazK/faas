package main_test

import (
	"encoding/json"
	"encoding/xml"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHTTPRestartMatrixExampleManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-restart-matrix-test.yaml", "http-restart-matrix", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"write-before-acceptance", "write-pending", "write-committed"}
	stages := []string{"before-acceptance", "pending", "committed"}
	if len(prepared) != len(names) {
		t.Fatalf("matrix omitted checkpoint: %+v", prepared)
	}
	plans := main.PrepareTestRunPlans(prepared, 1)
	if len(plans) != 3 {
		t.Fatalf("matrix scheduled %d runs", len(plans))
	}
	for i, p := range prepared {
		if p.Name != names[i] || p.Engine != "real-vm" || !reflect.DeepEqual(p.Profiles, []string{"warm"}) || plans[i].Prepared.Name != names[i] {
			t.Fatalf("checkpoint scheduling: %+v", p)
		}
		if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
			t.Fatal(err)
		}
		restart := p.Scenario.Steps[1].Restart
		if p.Scenario.Services["payment"].Secrets["HOLD_STAGE"] != stages[i] || restart == nil || restart.InFlight == nil {
			t.Fatal("checkpoint fixture or restart omitted")
		}
		want := map[string]any{"/received": true, "/accepted": i != 0, "/pending": i == 1, "/committed": i == 2, "/held": true}
		checkpoint := restart.InFlight.Checkpoints[1]
		if checkpoint.Workload != "payment" || checkpoint.Request.Name != "provider-"+stages[i] || !reflect.DeepEqual(checkpoint.Request.Expect.JSON, want) {
			t.Fatalf("wrong checkpoint assertions: %+v", checkpoint)
		}
		if !reflect.DeepEqual(p.Scenario.Steps[2].Command, []string{"node", "../test/in-flight-acceptance.mjs", stages[i]}) {
			t.Fatal("recovery did not reconcile the intended checkpoint")
		}
	}
}

func TestHTTPRestartMatrixCombinedReportsIdentifyFailedCheckpoint(t *testing.T) {
	main.CaptureSuiteTestOutput(t)
	names := []string{"write-before-acceptance", "write-pending", "write-committed"}
	results := make([]main.TestRunReceipt, 0, 3)
	for i, name := range names {
		status := "passed"
		if i == 1 {
			status = "failed"
		}
		results = append(results, main.TestRunReceipt{Suite: "http-restart-matrix", Scenario: name, Engine: "real-vm", Profile: "warm", Status: status, Steps: []main.TestScenarioStepEvidence{{Name: "replace-caller", Status: status, Restart: &main.TestRestartEvidence{Workload: "app", Status: status, InFlight: &main.TestRestartInFlightEvidence{Name: "interrupted-write", PendingAtRestart: true, Joined: true, Outcome: "request_ended"}}}}})
	}
	directory := t.TempDir()
	jsonPath, xmlPath, htmlPath := filepath.Join(directory, "results.json"), filepath.Join(directory, "results.xml"), filepath.Join(directory, "results.html")
	if code := main.FinishTestRunReports("http-restart-matrix", results, jsonPath, xmlPath, htmlPath); code != 1 {
		t.Fatal("partial checkpoint failure passed the matrix")
	}
	body, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var reports []main.TestRunReceipt
	if err := json.Unmarshal(body, &reports); err != nil || len(reports) != 3 {
		t.Fatalf("combined receipts: %v", err)
	}
	for i, r := range reports {
		if r.Scenario != names[i] || r.Status != results[i].Status || !r.Steps[0].Restart.InFlight.Joined {
			t.Fatalf("checkpoint result omitted: %+v", r)
		}
	}
	body, err = os.ReadFile(xmlPath)
	if err != nil {
		t.Fatal(err)
	}
	var junit main.TestJUnitSuite
	if err := xml.Unmarshal(body, &junit); err != nil || junit.Tests != 3 || junit.Failures != 1 || junit.Skipped != 0 || junit.Cases[1].Failure == nil {
		t.Fatalf("JUnit hid checkpoint failure: %+v, %v", junit, err)
	}
	body, err = os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.Contains(string(body), name) {
			t.Fatalf("HTML omitted checkpoint %s", name)
		}
	}
}
