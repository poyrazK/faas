package operation

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperationArtifactSpoolCapacityAndCleanup(t *testing.T) {
	budget := operationArtifactBudget{}
	for i := 0; i < api.OperationArtifactTransfersPerAccount; i++ {
		if !budget.reserve("one", 0) {
			t.Fatal("slot unavailable")
		}
	}
	if budget.reserve("one", 0) {
		t.Fatal("unbounded zero-byte transfers")
	}
	for i := 0; i < api.OperationArtifactTransfersPerAccount; i++ {
		if !budget.reserve("two", 0) {
			t.Fatal("other account blocked")
		}
	}
	if budget.reserve("three", 0) {
		t.Fatal("node transfer limit bypassed")
	}
	budget.release("one", 0)
	if !budget.reserve("three", 0) {
		t.Fatal("slot not released")
	}
	if budget.reserve("four", api.OperationArtifactSpoolMaxBytes) {
		t.Fatal("spool capacity bypassed")
	}
}
