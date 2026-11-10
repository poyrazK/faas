package storedworkflowtemplates_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storedworkflowtemplates"
	"gopkg.in/yaml.v3"
)

func TestStartersSimulateCompletely(t *testing.T) {
	for _, name := range storedworkflowtemplates.Names {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			files, err := storedworkflowtemplates.Materialize(name, dir)
			expectedFiles := 6
			if name == "approval-flow" {
				expectedFiles = 8
			}
			if err != nil || len(files) != expectedFiles {
				t.Fatalf("materialize: %v %v", files, err)
			}
			read := func(file string) []byte {
				data, err := os.ReadFile(filepath.Join(dir, file))
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(filepath.Join(dir, file))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0600 {
					t.Fatalf("unsafe file permissions: %v", info.Mode())
				}
				return data
			}
			request := api.SimulateStoredWorkflowRequest{Input: read("sample-input.json")}
			if err := yaml.Unmarshal(read("stored_workflow.yaml"), &request.Definition); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(read("mock-outputs.json"), &request.MockOutputs); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(read("mock-attempts.json"), &request.MockAttempts); err != nil {
				t.Fatal(err)
			}
			result, err := state.SimulateStoredWorkflow(context.Background(), request, api.PlanHobby)
			if err != nil || !result.DefinitionValid || !result.Complete || len(result.Issues) != 0 || len(result.Warnings) != 0 {
				t.Fatalf("starter simulation: %+v %v", result, err)
			}
			if name == "scheduled-report" && (request.Definition.Trigger.Enabled == nil || *request.Definition.Trigger.Enabled) {
				t.Fatal("schedule must start disabled")
			}
			if name == "approval-flow" {
				states := map[string]string{}
				for _, row := range result.Trace {
					states[row.StepName] = row.State
				}
				if states["approval"] != "timed_out" || states["fulfill"] != "skipped" || states["expire"] != "mocked" {
					t.Fatalf("timeout branch: %v", states)
				}
			}
			if _, err := storedworkflowtemplates.Materialize(name, dir); err == nil {
				t.Fatal("overwrote an existing starter")
			}
		})
	}
}

func TestRejectUnknownTemplateBeforeWriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "starter")
	if _, err := storedworkflowtemplates.Materialize("../contact-sync", dir); err == nil {
		t.Fatal("accepted unknown template")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("created destination: %v", err)
	}
}
