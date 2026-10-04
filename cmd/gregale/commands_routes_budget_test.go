package main

// ADR-447: budget synthesis requires captured version 2 route groups.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoutesPlanRejectsConcreteBudgetConsolidationBeforeAuth(t *testing.T) {
	resetJSONOut(t)
	path := filepath.Join(t.TempDir(), "requirements.yaml")
	if err := os.WriteFile(path, []byte(routePlanConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"routes", "plan", "my-api", "--requirements", path, "--consolidate-budgets"}); code != 1 {
		t.Fatal("concrete consolidation accepted")
	}
}
