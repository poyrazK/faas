package state_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestMemoryPressureQueryUsesKindConstants ties OperatorMemoryPressureEvents'
// SQL literals to the constants schedd writes, so renaming one without the
// other fails here instead of silently emptying the report.
func TestMemoryPressureQueryUsesKindConstants(t *testing.T) {
	raw, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatal(err)
	}
	q := string(raw)
	start := strings.Index(q, "-- name: OperatorMemoryPressureEvents ")
	if start < 0 {
		t.Fatal("OperatorMemoryPressureEvents not in queries.sql")
	}
	q = q[start:]
	if end := strings.Index(q[1:], "-- name: "); end >= 0 {
		q = q[:end+1]
	}
	for _, lit := range []string{
		state.MemoryPressureKindEviction, state.MemoryPressureKindFrozenPark, state.MemoryPressureKindOOMKill, state.MemoryPressureParkReason,
	} {
		if !strings.Contains(q, "'"+lit+"'") {
			t.Errorf("OperatorMemoryPressureEvents does not filter on %q", lit)
		}
	}
}
