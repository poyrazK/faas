package mcpmanagement

import (
	"context"
	"testing"
)

func TestValidateVerificationContractTool(t *testing.T) {
	m := &management{}
	out, err := m.validateVerificationContract(context.Background(), verifyContractArgs{YAML: "version: 1\nchecks:\n  - {name: health, http: {path: /healthz}}\n  - {name: order, advisory: true, http: {method: POST, path: /orders}}\n"})
	if err != nil {
		t.Fatal(err)
	}
	summary := out.(map[string]any)
	checks := summary["checks"].([]map[string]any)
	if len(checks) != 2 || checks[0]["required"] != true || checks[1]["previews_and_branches_only"] != true || checks[1]["required"] != false {
		t.Fatalf("summary = %+v", summary)
	}
	for name, args := range map[string]verifyContractArgs{
		"neither":  {},
		"both":     {YAML: "version: 1", Contract: map[string]any{"version": 1}},
		"invalid":  {YAML: "version: 1\nchecks: [{name: x, http: {path: nope}}]"},
		"bad json": {Contract: map[string]any{"version": 1, "checks": []any{map[string]any{"name": "x", "sql": map[string]any{"database": "main", "query": "select 1"}}}}},
	} {
		if _, err := m.validateVerificationContract(context.Background(), args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := m.validateVerificationContract(context.Background(), verifyContractArgs{Contract: map[string]any{"version": 1, "checks": []any{map[string]any{"name": "x", "http": map[string]any{"path": "/"}}}}}); err != nil {
		t.Fatalf("JSON contract: %v", err)
	}
}
