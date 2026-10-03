package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScenarioValidateChecksAllSourcesWithoutPlatform(t *testing.T) {
	dir := t.TempDir()
	for _, source := range []string{"api", "worker"} {
		path := filepath.Join(dir, source)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(`{"name":"scenario-test","version":"1.0.0","scripts":{"start":"node server.js"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "server.js"), []byte(`console.log("ready")`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	contents := "version: 1\nscenarios:\n  export:\n    project: export-api\n    source: ./api\n    services:\n      worker: {source: ./worker}\n    command: [node, test.js]\n"
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if code := cmdTest([]string{"--validate", "--manifest", manifest}); code != 0 {
		t.Fatalf("valid manifest exit = %d", code)
	}
	if err := os.Remove(filepath.Join(dir, "worker", "package.json")); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--validate", "--manifest", manifest}); code == 0 {
		t.Fatal("missing worker manifest passed validation")
	}
}

func TestScenarioJUnitDistinguishesEngineAndCleanupFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.xml")
	receipts := []testRunReceipt{
		{Scenario: "export", Profile: "simulated", Engine: "simulated", Attempt: 1, Status: "passed", DurationMS: 25},
		{Scenario: "export", Profile: "restored", Engine: "real-vm", Attempt: 2, Status: "failed", Error: "restore used cold_boot", CleanupError: "destroy worker failed", DurationMS: 1500, StartedAt: time.Now().UTC()},
	}
	if err := writeTestJUnit(path, receipts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Tests != 2 || suite.Failures != 1 || suite.Cases[0].ClassName != "gregale.scenario.simulated" || suite.Cases[1].Name != "export/restored#2" ||
		suite.Cases[1].ClassName != "gregale.scenario.real-vm" || suite.Cases[1].Failure == nil ||
		!strings.Contains(suite.Cases[1].Failure.Message, "destroy worker failed") {
		t.Fatalf("junit suite = %+v", suite)
	}
}
