package main

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// The reviewed allowlist is a promotion gate, not a post-promotion registry
// check. Protocol discovery itself never grants permission to execute fixtures.
func validateMCPReviewedToolCatalog(cfg mcphosting.Config, report mcphosting.Report) error {
	if !report.OK || len(cfg.Auth.ToolScopes) == 0 || len(report.Tools) != len(cfg.Auth.ToolScopes) {
		return errors.New("candidate tool catalog does not exactly match the reviewed tool_scopes; fixtures and promotion blocked")
	}
	seen := map[string]bool{}
	for _, tool := range report.Tools {
		if _, ok := cfg.Auth.ToolScopes[tool.Name]; !ok || seen[tool.Name] {
			return fmt.Errorf("candidate exposes unreviewed or duplicate tool %q; fixtures and promotion blocked", tool.Name)
		}
		seen[tool.Name] = true
	}
	return nil
}
