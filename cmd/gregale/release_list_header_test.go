package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-19: `release list` printed two unlabeled 32-hex columns.
func TestDeploymentHeaderNamesTheColumns(t *testing.T) {
	for _, wide := range []bool{false, true} {
		var buf bytes.Buffer
		main.RenderDeploymentHeader(&buf, wide)
		got := buf.String()
		if !strings.HasPrefix(got, "DEPLOYMENT") || !strings.Contains(got, "APP ID") {
			t.Fatalf("wide=%v header = %q", wide, got)
		}
		if wide && !strings.Contains(got, "REV") {
			t.Fatalf("wide header has no revision column: %q", got)
		}
	}
}
