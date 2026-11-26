package operatorobs

import (
	"testing"
	"time"
)

// lengthenRuntimeCollectionBudget gives a full-set coverage test a minute
// instead of the 3 s production budget. These tests read 40-50 instances
// through the fixture server; on a loaded race runner the shared deadline
// expired part-way and the instances it had not reached read as
// unavailable (nightly 2026-11-25, ci-light 2026-11-25). Tests of the
// budget itself keep the production value.
func lengthenRuntimeCollectionBudget(t *testing.T) {
	t.Helper()
	budget := runtimeCollectionBudget
	runtimeCollectionBudget = time.Minute
	t.Cleanup(func() { runtimeCollectionBudget = budget })
}
