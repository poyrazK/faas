package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAutomationCheckEvidenceValidation(t *testing.T) {
	definition := WorkflowSpec{Name: "test", Steps: []WorkflowStepSpec{{Name: "send", Path: "/send"}}}
	raw, _ := json.Marshal(definition)
	now := time.Now().UTC()
	valid := AutomationCheckEvidence{DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(raw)), CheckedVersion: 7, CheckedAt: now, CoveragePassed: true, Exclusions: []AutomationCheckExclusion{}, Scenarios: []AutomationCheckScenario{{Name: "success", Passed: true, DefinitionValid: true, Complete: true}}}
	if err := valid.Validate(definition, 7, now); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name   string
		modify func(*AutomationCheckEvidence)
	}{
		{"hash", func(e *AutomationCheckEvidence) { e.DefinitionHash = strings.Repeat("0", 64) }},
		{"version", func(e *AutomationCheckEvidence) { e.CheckedVersion++ }},
		{"future", func(e *AutomationCheckEvidence) { e.CheckedAt = now.Add(time.Hour) }},
		{"old", func(e *AutomationCheckEvidence) { e.CheckedAt = now.Add(-25 * time.Hour) }},
		{"missing exclusions", func(e *AutomationCheckEvidence) { e.Exclusions = nil }},
		{"missing scenarios", func(e *AutomationCheckEvidence) { e.Scenarios = nil }},
		{"duplicate", func(e *AutomationCheckEvidence) { e.Scenarios = append(e.Scenarios, e.Scenarios[0]) }},
		{"failed", func(e *AutomationCheckEvidence) { e.Scenarios[0].Passed = false }},
		{"incomplete", func(e *AutomationCheckEvidence) { e.Scenarios[0].Complete = false }},
		{"coverage inconsistent", func(e *AutomationCheckEvidence) { e.CoverageRemaining = 1 }},
		{"coverage gate failed", func(e *AutomationCheckEvidence) {
			e.CoverageRemaining = 1
			e.CoveragePassed = false
			e.CoverageRequired = true
		}},
		{"control chars", func(e *AutomationCheckEvidence) { e.Scenarios[0].Name = "bad\x1b[31m" }},
		{"exclusion", func(e *AutomationCheckEvidence) {
			e.Exclusions = []AutomationCheckExclusion{{Step: "send", Code: "unknown", Reason: "reviewed"}}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			e := valid
			e.Scenarios = append([]AutomationCheckScenario(nil), valid.Scenarios...)
			change.modify(&e)
			if e.Validate(definition, 7, now) == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
	valid.CoverageRemaining = 1
	valid.CoveragePassed = false
	if err := valid.Validate(definition, 7, now); err != nil {
		t.Fatal("advisory coverage must allow gaps", err)
	}
}
