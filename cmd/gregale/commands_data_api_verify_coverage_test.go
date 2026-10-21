package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPIVerifyCoverageRequiresPassingMappedEvidenceAndCatchesNewRelations(t *testing.T) {
	raw := main.DataAPIContractFixture(t, main.DataAPIDiffFixture())
	contract, err := main.ParseDataAPIContract(raw)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	policyPath := write("policy.json", map[string]any{"version": 1, "default_required": []string{"integrity", "access_read"}, "relations": []any{map[string]any{"relation": "api.notes", "required": []string{"integrity", "access_read", "performance"}, "checks": []any{map[string]any{"kind": "integrity", "name": "Bodies"}, map[string]any{"kind": "access_read", "scenario": "default", "name": "Owner read"}, map[string]any{"kind": "performance", "scenario": "default", "name": "Read query"}}}}})
	policy, err := main.CoveragePolicy(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	verification := map[string]any{"version": 1, "command": "data-api verify", "status": "passed", "cleanup": "passed", "authorization": "passed", "schema_fingerprint": contract.Fingerprint, "checks": []any{map[string]any{"name": "Bodies", "passed": true}}, "access_checks": []any{map[string]any{"name": "Owner read", "scenario": "default", "passed": true}}}
	plans := map[string]any{"version": 1, "command": "data-api explain", "status": "passed", "cleanup": "passed", "schema_fingerprint": contract.Fingerprint, "budget_check": map[string]any{"status": "passed", "checked": 1, "violations": []any{}}, "results": []any{map[string]any{"name": "Read query", "scenario": "default", "status": "planned", "relation": "api.notes"}}}
	verifyPath := write("verify.json", verification)
	planPath := write("plans.json", plans)
	v, err := main.ReadCoverageEvidence(verifyPath, "data-api verify", contract.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	p, err := main.ReadCoverageEvidence(planPath, "data-api explain", contract.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	report, err := main.EvaluateDataAPICoverage(contract, policy, v, p, nil)
	if err != nil || report.Gaps != 0 {
		t.Fatalf("passing evidence rejected: %+v %v", report, err)
	}
	policy.Relations[0].Checks = append(policy.Relations[0].Checks, main.DataAPICoverageMapping{Kind: "integrity", Name: "Missing assertion"})
	incomplete, _ := main.EvaluateDataAPICoverage(contract, policy, v, p, nil)
	if incomplete.Gaps != 1 {
		t.Fatal("one passing mapping masked a missing mapping")
	}
	policy.Relations[0].Checks = policy.Relations[0].Checks[:len(policy.Relations[0].Checks)-1]
	p.Budget = nil
	v.Access[0].Skipped = true
	report, _ = main.EvaluateDataAPICoverage(contract, policy, v, p, nil)
	if report.Gaps != 2 {
		t.Fatalf("unchecked or skipped evidence counted: %+v", report)
	}
	report, _ = main.EvaluateDataAPICoverage(contract, policy, nil, nil, nil)
	if report.Gaps != 3 {
		t.Fatal("missing evidence counted")
	}
	current := contract
	current.Parsed.Tables = append(append([]main.DataAPITable{}, contract.Parsed.Tables...), main.DataAPITable{Schema: "api", Name: "orders"})
	report, _ = main.EvaluateDataAPICoverage(current, policy, nil, nil, &contract)
	if report.Gaps != 5 || !report.Relations[1].New {
		t.Fatalf("new table lost default requirements: %+v", report)
	}
	verification["schema_fingerprint"] = strings.Repeat("0", 64)
	write("verify.json", verification)
	if _, err = main.ReadCoverageEvidence(verifyPath, "data-api verify", contract.Fingerprint); err == nil {
		t.Fatal("stale evidence accepted")
	}
	schemaPath := filepath.Join(dir, "schema.json")
	_ = os.WriteFile(schemaPath, raw, 0600)
	if main.CmdDataAPIVerifyCoverage([]string{"--schema", schemaPath, "--policy", policyPath, "--check"}) == 0 {
		t.Fatal("CI passed without evidence")
	}
	if main.CmdDataAPIVerifyCoverage([]string{"--schema", schemaPath, "--policy", policyPath}) != 0 {
		t.Fatal("informational coverage failed")
	}
}
func TestDataAPIVerifyCoverageRejectsAmbiguousPolicies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	for _, raw := range []string{`{}`, `{"version":1,"default_required":["typo"],"relations":[]}`, `{"version":1,"default_required":["integrity"],"extra":true}`, `{"version":1,"default_required":["integrity"],"relations":[{"relation":"api.notes","required":null}]}`, `{"version":1,"default_required":["integrity"],"relations":[{"relation":"api.notes","required":["access_read"],"checks":[{"kind":"access_read","name":"read"}]}]}`} {
		_ = os.WriteFile(path, []byte(raw), 0600)
		if _, err := main.CoveragePolicy(path); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
