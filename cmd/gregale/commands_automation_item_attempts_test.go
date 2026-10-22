package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationItemAttemptFiles(t *testing.T) {
	for _, document := range []string{`null`, `[]`, `{"batch":{}}`, `{"batch":{"01":[{"outcome":"success","output":null}]}}`, `{"batch":{"128":[{"outcome":"success","output":null}]}}`, `{"batch":{"0":[]}}`, `{"batch":{"0":[{"outcome":"success","unknown":true}]}}`} {
		path := filepath.Join(t.TempDir(), "attempts.json")
		if err := os.WriteFile(path, []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := main.ReadAutomationItemMockAttempts(path); err == nil {
			t.Fatalf("accepted %s", document)
		}
	}
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	output := main.CaptureAutomationStdout(t)
	dir := t.TempDir()
	definition := filepath.Join(dir, "automation.yaml")
	if err := os.WriteFile(definition, []byte("name: batch\nsteps:\n  - name: sync\n    for_each:\n      items: input.items\n      action:\n        run: send\n        retry: {max_attempts: 2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	attempts := filepath.Join(dir, "attempts.json")
	document := `{"sync":{"0":[{"outcome":"failure","error":"private-value"},{"outcome":"success","output":null}]}}`
	if err := os.WriteFile(attempts, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	fake := main.AuthedFakeAPI(t, `{"definition_valid":true,"complete":true,"issues":[],"warnings":[],"trace":[]}`, 200)
	if code := main.CmdAutomationsSimulate([]string{"--app", "billing", "--file", definition, "--mock-item-attempts-file", attempts}); code != 0 {
		t.Fatal(code)
	}
	var request api.SimulateAutomationRequest
	if err := json.Unmarshal((*fake.SawBody()), &request); err != nil || len(request.MockItemAttempts["sync"]["0"]) != 2 || string(request.MockItemAttempts["sync"]["0"][1].Output) != "null" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	if strings.Contains(output.String(), "private-value") {
		t.Fatal("report leaked mock error")
	}
	suite := filepath.Join(dir, "scenarios.yaml")
	if err := os.WriteFile(suite, []byte("definition: automation.yaml\nscenarios:\n  - name: retry\n    mock_item_attempts_file: attempts.json\n    expect:\n      sync: {state: resolved}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, err := main.LoadAutomationScenarios(suite)
	if err != nil || len((*scenarios[0].Request()).MockItemAttempts["sync"]["0"]) != 2 {
		t.Fatalf("scenarios=%+v err=%v", scenarios, err)
	}
}
