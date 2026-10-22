package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataRepairNamedPolicyCLIFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checks.json")
	valid := `{"version":1,"checks":[{"name":"Declared rule","sql":"SELECT true"}]}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := main.DataRepairCheckPolicyFile(path)
	if err != nil || len(policy.Checks) != 1 || policy.Checks[0].Name != "Declared rule" {
		t.Fatal("named policy file", err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", api.DataRepairMaxRequestBytes)+valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := main.DataRepairCheckPolicyFile(path); err == nil {
		t.Fatal("oversized policy file accepted")
	}
	// Selection fails before any application database or executor is contacted.
	if main.CmdDataRepair([]string{"serve", "--checks-file", "legacy.sql", "--checks-policy-file", path}) == 0 {
		t.Fatal("mixed check authorities accepted")
	}
}
