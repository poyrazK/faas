package rehearsalpg

import (
	"encoding/json"
	"strings"
	"testing"
)

func evidenceForTest(t *testing.T, rows ...string) *Evidence {
	t.Helper()
	s := Spec{Version: 1, FixtureSHA256: strings.Repeat("a", 64), Tables: []Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 50}
	before := []Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}
	after := []Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}
	for _, r := range rows {
		after[0].Rows = append(after[0].Rows, json.RawMessage(r))
	}
	a, err := CanonicalSnapshots(after)
	if err != nil {
		t.Fatal(err)
	}
	e := &Evidence{Receipt: Receipt{Version: 1, RunID: strings.Repeat("b", 32), AppID: "app", ResourceID: "resource", FixtureSHA256: s.FixtureSHA256, ContractSHA256: s.ContractSHA256(), InitialSHA256: Hash(before)}, Status: "complete", Provenance: Provenance, DeploymentID: "deployment", Spec: s, Before: before, After: a, Changes: Changes(before, a), LockSamples: 1, LockBudgetPassed: true, CleanupConfirmed: true}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPostgresComparisonDetectsDuplicateInsertAndPinsStartingData(t *testing.T) {
	one := evidenceForTest(t, `{"item":"a"}`)
	two := evidenceForTest(t, `{"item":"a"}`, `{"item":"a"}`)
	if c := Compare(one, two); c.Status != "failed" || len(c.After[0].Inserted) != 2 {
		t.Fatalf("duplicate was lost: %+v", c)
	}
	two = evidenceForTest(t, `{"item":"a"}`)
	if c := Compare(one, two); c.Status != "passed" {
		t.Fatalf("same state changed: %+v", c)
	}
	two.Before[0].Rows = append(two.Before[0].Rows, json.RawMessage(`{"item":"seed"}`))
	two.InitialSHA256 = Hash(two.Before)
	two.Changes = Changes(two.Before, two.After)
	if c := Compare(one, two); c.Status != "inconclusive" {
		t.Fatalf("changed initial data passed: %+v", c)
	}
}
func TestPostgresComparisonRejectsIncompleteOrRewrittenEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Evidence)
	}{
		{"cleanup", func(e *Evidence) { e.CleanupConfirmed = false }},
		{"collector", func(e *Evidence) { e.LockSamples = 0 }},
		{"untrusted", func(e *Evidence) { e.Provenance = "application_reported" }},
		{"changes", func(e *Evidence) { e.Changes = nil }},
		{"missing table", func(e *Evidence) { e.After = nil }},
		{"columns", func(e *Evidence) {
			e.After[0].Rows = []json.RawMessage{json.RawMessage(`{"other":"a"}`)}
			e.Changes = Changes(e.Before, e.After)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := evidenceForTest(t, `{"item":"a"}`)
			b := evidenceForTest(t, `{"item":"a"}`)
			tc.mutate(b)
			if c := Compare(a, b); c.Status != "inconclusive" {
				t.Fatalf("incomplete evidence passed: %+v", c)
			}
		})
	}
}
func TestPostgresLockBudgetIsAnAssertion(t *testing.T) {
	a := evidenceForTest(t)
	b := evidenceForTest(t)
	b.MaxObservedLockWaitMS = 30
	if c := Compare(a, b); c.Status != "passed" {
		t.Fatal(c)
	}
	b.MaxObservedLockWaitMS = 51
	b.LockBudgetPassed = false
	if c := Compare(a, b); c.Status != "failed" {
		t.Fatal(c)
	}
}
