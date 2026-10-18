package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDataAPIConsumerImpacts(t *testing.T) {
	consumers := []dataAPIConsumer{{Kind: "saved_view", Name: "orders", Dependencies: []string{"api.orders.total", "api.orders"}}, {Kind: "rpc", Name: "submit", Dependencies: []string{"api.submit"}}, {Kind: "rpc", Name: "unrelated", Dependencies: []string{"api.orders_archive"}}}
	changes := []dataAPIChange{{Path: "api.orders", Breaking: true}, {Path: "api.submit(id)", Breaking: true, Approved: true}}
	impacts := dataAPIConsumerImpacts(consumers, changes)
	if len(impacts) != 2 || impacts[0].Name != "submit" || !impacts[0].Changes[0].Approved || len(impacts[1].Changes) != 1 {
		t.Fatalf("impacts: %+v", impacts)
	}
	if len(dataAPIConsumerImpacts(consumers, nil)) != 0 {
		t.Fatal("unchanged contracts have impacts")
	}
}
func TestDataAPIConsumerManifest(t *testing.T) {
	for _, raw := range []string{`{"version":2,"consumers":[]}`, `{"version":1}`, `{"version":1,"consumers":[],"typo":true}`, `{"version":1,"consumers":[]} {}`, `{"version":1,"consumers":[{"kind":"rpc","name":"a","dependencies":[]}]}`, `{"version":1,"consumers":[{"kind":"rpc","name":"a","dependencies":["api.a"]},{"kind":"rpc","name":"a","dependencies":["api.a"]}]}`} {
		path := filepath.Join(t.TempDir(), "dependencies.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readDataAPIConsumers(path); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	path := filepath.Join(t.TempDir(), "dependencies.json")
	os.WriteFile(path, []byte(`{"version":1,"consumers":[{"kind":"saved_view","name":"orders","dependencies":["api.orders.total"]}]}`), 0600)
	if consumers, err := readDataAPIConsumers(path); err != nil || len(consumers) != 1 {
		t.Fatalf("%+v %v", consumers, err)
	}
}

func TestDataAPIImpactOfflineGate(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	root := t.TempDir()
	before := dataAPIDiffFixture()
	after := dataAPIDiffFixture()
	after.Tables[0].Columns[0].Type = "number"
	baseline := filepath.Join(root, "before.json")
	current := filepath.Join(root, "after.json")
	deps := filepath.Join(root, "deps.json")
	for path, content := range map[string][]byte{baseline: dataAPIContractFixture(t, before), current: dataAPIContractFixture(t, after), deps: []byte(`{"version":1,"consumers":[{"kind":"saved_view","name":"Notes","dependencies":["api.notes.body"]}]}`)} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code := cmdDataAPI([]string{"diff", "--baseline", baseline, "--current", current, "--dependencies", deps, "--check"}); code != 1 {
		t.Fatalf("breaking gate: %d", code)
	}
	var report struct {
		Impacts  []dataAPIImpact `json:"impacts"`
		Coverage string          `json:"dependency_coverage"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Coverage != "declared" || len(report.Impacts) != 1 || report.Impacts[0].Name != "Notes" {
		t.Fatalf("report: %+v", report)
	}
}

func TestDataAPIConsumerUnknownPaths(t *testing.T) {
	consumers := []dataAPIConsumer{{Kind: "saved_view", Name: "Notes", Dependencies: []string{"api.notes.body", "api.echo(value)"}}}
	before := dataAPIDiffFixture()
	after := dataAPIDiffFixture()
	after.Tables = nil
	if err := validateDataAPIConsumers(consumers, before, after); err != nil {
		t.Fatal(err)
	}
	consumers[0].Dependencies = append(consumers[0].Dependencies, "api.notes.typo")
	if err := validateDataAPIConsumers(consumers, before, after); err == nil {
		t.Fatal("unknown dependency accepted")
	}
}
