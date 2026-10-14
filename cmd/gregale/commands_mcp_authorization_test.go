package main

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPAuthorizationPolicyPreflight(t *testing.T) {
	dir := t.TempDir()
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{{Name: "read", InputSchema: map[string]any{"type": "object"}}}})
	body, _ := mcphosting.MarshalContract(baseline)
	if err := os.WriteFile(filepath.Join(dir, "reader.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("READER_TOKEN", "ROLE_TOKEN_SENTINEL")
	t.Setenv("SCOPE_TOKEN", "SCOPE_TOKEN_SENTINEL")
	t.Setenv("SAME_TOKEN", "ROLE_TOKEN_SENTINEL")
	for _, tc := range []struct {
		authorization string
		positive      bool
		valid         bool
	}{
		{`{}`, true, true},
		{`{"denied_tools":["write"],"denied_resources":["file:///secret"],"denied_resource_templates":["file:///secret/{id}"],"denied_prompts":["admin"],"insufficient_scope_token_env":"SCOPE_TOKEN"}`, true, true},
		{`{"denied_tools":["read"]}`, true, false},
		{`{"denied_tools":[""]}`, true, false},
		{`{"denied_tools":["write","write"]}`, true, false},
		{`{"insufficient_scope_token_env":"SAME_TOKEN"}`, true, false},
		{`{"insufficient_scope_token_env":"MISSING_TOKEN"}`, true, false},
		{`{"insufficient_scope_token_env":"INLINE_TOKEN_SENTINEL.with.dots"}`, true, false},
		{`{"token":"inline-secret"}`, true, false},
		{`{}`, false, false},
	} {
		role := map[string]any{"name": "reader", "baseline": "reader.json", "authorization": json.RawMessage(tc.authorization)}
		if tc.positive {
			role["token_env"] = "READER_TOKEN"
		}
		policy, _ := json.Marshal(map[string]any{"version": 1, "roles": []any{role}})
		path := filepath.Join(dir, "policy.json")
		if err := os.WriteFile(path, policy, 0600); err != nil {
			t.Fatal(err)
		}
		roles, err := loadMCPReleasePolicy(path)
		if (err == nil) != tc.valid {
			t.Fatalf("policy %s result=%v", tc.authorization, err)
		}
		if err != nil && strings.Contains(err.Error(), "TOKEN_SENTINEL") {
			t.Fatal("preflight leaked credential")
		}
		if err == nil {
			encoded, _ := json.Marshal(roles)
			if strings.Contains(string(encoded), "TOKEN_SENTINEL") {
				t.Fatal("policy serialized credential")
			}
		}
	}
}
