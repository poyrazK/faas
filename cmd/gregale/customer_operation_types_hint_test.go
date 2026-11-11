package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-82: `operation types --check` said to run "customer-operations
// types", a spelling customers do not type.
func TestOperationTypesCheckNamesTheCommand(t *testing.T) {
	for _, status := range []string{"missing", "stale"} {
		got := main.CustomerOperationTypesMessage(main.CustomerOperationTypesResult{Output: "types.d.ts", Status: status}, true)
		if !strings.Contains(got, "run gregale operation types") || strings.Contains(got, "customer-operations") {
			t.Fatalf("%s: %q", status, got)
		}
	}
}
