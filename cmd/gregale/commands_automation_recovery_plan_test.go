package main_test

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomationRecoveryPlanSavedAndRetried(t *testing.T) {
	main.ResetJSONOut(t)
	fake := automationRecoverServer(t, "lost_reply")
	dir := t.TempDir()
	plan := filepath.Join(dir, "plan.json")
	args := append(automationRecoverArgs(), "--plan-out", plan)
	if code := main.CmdAutomations(args); code != 0 {
		t.Fatalf("save=%d", code)
	}
	calls, _ := fake.snapshot()
	for _, call := range calls {
		if call.Method != "GET" {
			t.Fatal("plan creation mutated run")
		}
	}
	data, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE") {
		t.Fatal("private data in plan")
	}
	info, _ := os.Stat(plan)
	if info.Mode().Perm() != 0600 {
		t.Fatal("plan permissions")
	}
	p, err := main.ReadAutomationRecoveryPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if p.Runs[0].ExpectedResumeCount != 2 {
		t.Fatal("count not pinned")
	}
	if code := main.CmdAutomations(args); code == 0 {
		t.Fatal("overwrote plan")
	}
	journal := filepath.Join(dir, "attempt1.jsonl")
	if code := main.CmdAutomations([]string{"recovery-plan", "--plan", plan, "--journal", journal}); code != 1 {
		t.Fatalf("partial=%d", code)
	}
	content, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	var kinds []string
	for decoder.More() {
		var e main.AutomationRecoveryJournalEntry
		if err := decoder.Decode(&e); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "started,attempted,outcome,attempted,outcome,completed" {
		t.Fatalf("journal=%v", kinds)
	}
	if strings.Contains(string(content), "PRIVATE") {
		t.Fatal("private journal data")
	}
	fake.mu.Lock()
	fake.mode = "failed_again"
	fake.mu.Unlock()
	if code := main.CmdAutomations([]string{"recovery-plan", "--plan", plan, "--journal", filepath.Join(dir, "attempt2.jsonl")}); code != 1 {
		t.Fatalf("retry=%d", code)
	}
	_, accepted := fake.snapshot()
	if accepted[inspectorRunID] != 1 || accepted[recoverySecondRunID] != 1 {
		t.Fatalf("duplicate recovery: %v", accepted)
	}
	before, _ := fake.snapshot()
	if code := main.CmdAutomations([]string{"recovery-plan", "--plan", plan, "--journal", journal}); code != 1 {
		t.Fatal("overwrote journal")
	}
	after, _ := fake.snapshot()
	if len(before) != len(after) {
		t.Fatal("existing journal allowed requests")
	}
}

func TestAutomationRecoveryPlanRejectsInvalidFiles(t *testing.T) {
	p := main.AutomationRecoveryPlan{Version: 1, App: "billing", AppID: inspectorAppID, Name: "approval-flow", Key: "key", CreatedAt: time.Now().UTC(), Runs: []main.AutomationRecoverySelection{{ID: inspectorRunID, ExpectedResumeCount: 2}}}
	valid, _ := json.Marshal(p)
	for _, data := range []string{string(valid) + " {}", strings.Replace(string(valid), `"version":1`, `"version":2`, 1), strings.Replace(string(valid), `"version":1`, `"unknown":true,"version":1`, 1), strings.Repeat(" ", 65537)} {
		path := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := main.ReadAutomationRecoveryPlan(path); err == nil {
			t.Fatal("accepted invalid plan")
		}
	}
	p.Runs = append(p.Runs, p.Runs[0])
	if main.ValidateAutomationRecoveryPlan(p) == nil {
		t.Fatal("accepted duplicates")
	}
}

func TestAutomationRecoveryPlanJournalFailureStopsBeforeWrites(t *testing.T) {
	fake := automationRecoverServer(t, "success")
	c, err := main.AuthedClient()
	if err != nil {
		t.Fatal(err)
	}
	p := main.AutomationRecoveryPlan{Version: 1, App: "billing", AppID: inspectorAppID, Name: "approval-flow", Key: "key", CreatedAt: time.Now().UTC(), Runs: []main.AutomationRecoverySelection{{ID: inspectorRunID, ExpectedResumeCount: 2}}}
	f, err := os.CreateTemp(t.TempDir(), "journal")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := main.ExecuteAutomationRecoveryPlan(context.Background(), c, p, f); err == nil {
		t.Fatal("ignored journal failure")
	}
	calls, _ := fake.snapshot()
	for _, call := range calls {
		if call.Method != "GET" {
			t.Fatal("mutated with unavailable journal")
		}
	}
	p.AppID = recoverySecondRunID
	f, err = os.CreateTemp(t.TempDir(), "journal")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := main.ExecuteAutomationRecoveryPlan(context.Background(), c, p, f); err == nil {
		t.Fatal("ignored app identity change")
	}
}
