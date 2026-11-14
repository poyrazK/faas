package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdStoredWorkflowsInitCreatesLocalStarter(t *testing.T) {
	main.ResetJSONOut(t)
	output := main.CaptureStoredWorkflowStdout(t)
	dir := filepath.Join(t.TempDir(), "report")
	if code := main.CmdStoredWorkflows([]string{"init", "--template", "scheduled-report", "--path", dir}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if _, ok := main.ReadStoredWorkflowDefinition(filepath.Join(dir, "stored_workflow.yaml")); !ok {
		t.Fatal("generated definition cannot be loaded by CLI")
	}
	if !strings.Contains(output.String(), "README.md") {
		t.Fatalf("missing setup instructions: %s", output.String())
	}
	marker := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdStoredWorkflowsInit([]string{"--template", "contact-sync", "--path", dir}); code != 1 {
		t.Fatalf("overwrote existing destination: %d", code)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing files changed: %q %v", data, err)
	}
}

func TestCmdStoredWorkflowsInitJSONAndDiscovery(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	output := main.CaptureStoredWorkflowStdout(t)
	if code := main.CmdStoredWorkflowsInit([]string{"--list"}); code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	var list struct {
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal(output.Bytes(), &list); err != nil || len(list.Templates) != 4 {
		t.Fatalf("list=%s err=%v", output.String(), err)
	}
	output.Reset()
	dir := filepath.Join(t.TempDir(), "approval")
	if code := main.CmdStoredWorkflowsInit([]string{"--template", "approval-flow", "--path", dir}); code != 0 {
		t.Fatalf("create exit=%d", code)
	}
	var receipt struct {
		Template string   `json:"template"`
		Path     string   `json:"path"`
		Files    []string `json:"files"`
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || receipt.Template != "approval-flow" || receipt.Path != dir || len(receipt.Files) != 8 {
		t.Fatalf("receipt=%s err=%v", output.String(), err)
	}
	for _, args := range [][]string{nil, {"--list", "--template", "contact-sync"}, {"--template", "unknown"}, {"--list", "unexpected"}} {
		if code := main.CmdStoredWorkflowsInit(args); code != 1 {
			t.Fatalf("accepted %v: %d", args, code)
		}
	}
}
