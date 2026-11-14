package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPReviewedToolCatalogGatesFixtureExecution(t *testing.T) {
	var cfg mcphosting.Config
	cfg.Auth.ToolScopes = mcphosting.ToolScopePolicy{"add": {}, "subtract": {}}
	for _, tc := range []struct {
		name  string
		tools []string
		ok    bool
	}{
		{"reviewed set", []string{"subtract", "add"}, true},
		{"extra tool", []string{"add", "subtract", "delete_everything"}, false},
		{"substituted tool", []string{"add", "delete_everything"}, false},
		{"missing tool", []string{"add"}, false},
		{"duplicate tool", []string{"add", "add"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := mcphosting.Report{OK: true}
			for _, name := range tc.tools {
				report.Tools = append(report.Tools, mcphosting.Tool{Name: name})
			}
			if err := main.ValidateMCPReviewedToolCatalogForTest(cfg, report); (err == nil) != tc.ok {
				t.Fatalf("gate: %v", err)
			}
		})
	}
}
