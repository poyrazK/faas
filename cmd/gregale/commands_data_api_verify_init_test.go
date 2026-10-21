package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPIVerifyInitCreatesReviewablePackageAndPreservesInputs(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.json")
	config := filepath.Join(dir, "config.json")
	plans := filepath.Join(dir, "plans.json")
	output := filepath.Join(dir, "package")
	raw := dataAPIContractFixture(t, dataAPIDiffFixture())
	if err := os.WriteFile(schema, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"version":1,"identities":{"bob":{"subject":"private-subject"},"alice":{"claims":{"secret":"private-claim"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	contract, err := parseDataAPIContract(raw)
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"version": 1, "command": "data-api explain", "status": "passed", "cleanup": "passed", "workload_fingerprint": strings.Repeat("a", 64), "dataset_fingerprint": strings.Repeat("b", 64), "schema_fingerprint": contract.Fingerprint, "results": []any{map[string]any{"scenario": "default", "name": "Read", "status": "planned", "query_fingerprint": strings.Repeat("c", 64), "plan": map[string]any{"total_cost": 2.5}, "metrics": map[string]any{"estimated_scan_rows": 10}, "filter": "private-query-value"}}}
	content, _ := json.Marshal(report)
	_ = os.WriteFile(plans, content, 0600)
	if err := buildVerificationPackage(schema, config, plans, output); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"data-checks.json", "access-checks.json", "budgets.json", "coverage.json", "README.md"} {
		content, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private-subject", "private-claim", "private-query-value"} {
			if strings.Contains(string(content), secret) {
				t.Fatalf("copied private value into %s", name)
			}
		}
	}
	content, _ = os.ReadFile(filepath.Join(output, "access-checks.json"))
	if !strings.Contains(string(content), "{{fixture_value}}") {
		t.Fatal("access draft can pass without fixture input")
	}
	if err := buildVerificationPackage(schema, config, plans, output); err == nil {
		t.Fatal("existing output accepted")
	}
	preserved, _ := os.ReadFile(schema)
	if string(preserved) != string(raw) {
		t.Fatal("input schema changed")
	}
	report["schema_fingerprint"] = strings.Repeat("0", 64)
	content, _ = json.Marshal(report)
	_ = os.WriteFile(plans, content, 0600)
	bad := filepath.Join(dir, "bad")
	if err := buildVerificationPackage(schema, config, plans, bad); err == nil {
		t.Fatal("stale plans accepted")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("invalid input created package")
	}
}
func TestDataAPIVerifyInitForeignKeysQuoteCompositeColumnsAndIgnoreUnknownTargets(t *testing.T) {
	source := dataAPITable{Schema: "api", Name: "children", Columns: []dataAPIColumn{{Name: "parent"}, {Name: "tenant"}}, Relationships: json.RawMessage(`[{"foreignKeyName":"fk","columns":["parent","tenant"],"referencedRelation":"parents","referencedColumns":["id","tenant"]}]`)}
	target := dataAPITable{Schema: "api", Name: "parents", Columns: []dataAPIColumn{{Name: "id"}, {Name: "tenant"}}}
	sql := verificationForeignKeys(source, []dataAPITable{source, target})
	if len(sql) != 1 || !strings.Contains(sql[0], `source."parent" IS NOT NULL AND source."tenant" IS NOT NULL`) || !strings.Contains(sql[0], `target."tenant"=source."tenant"`) {
		t.Fatalf("wrong composite check: %v", sql)
	}
	if len(verificationForeignKeys(source, []dataAPITable{source})) != 0 {
		t.Fatal("unknown target inferred")
	}
}
