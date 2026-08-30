package sched

// drain_compile_test.go — compile-time guarantee the unified
// invocations drain participates in the pkg/dispatch contract
// (ADR-134 §6.7). The two schedd drains (this package's drain.go
// for invocations, dispatch_triggers.go for trigger_records) are
// the only writers of the row-state-machine transitions. Adding
// a row type that does not satisfy dispatch.Job should fail to
// compile here, not at runtime in production.
//
// PR-B replaces the synthetic invocationJob adapter with the real
// state.InvocationJobAdapter (pkg/state/invocation_job_adapter.go)
// — the adapter proxies ID/AppID/AccountID to the Invocation
// fields (Go forbids field/method same-name collision) and
// inherits the other Job methods via embedding. If a future change
// to dispatch.Job breaks the adapter signature, this test fails
// to compile and surfaces the change in code review.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/dispatch"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDispatch_ContractCompiles is the package-level assertion
// that the invocations drain can consume the dispatch contract.
// state.InvocationJobAdapter wraps *state.Invocation to satisfy
// dispatch.Job; if the interface changes (a method renamed, a
// signature altered) this test fails to compile, surfacing the
// change in code review rather than crashing at runtime inside
// the drain.
func TestDispatch_ContractCompiles(t *testing.T) {
	var _ dispatch.Job = (*state.InvocationJobAdapter)(nil)

	// Touch ctx so the import survives even if a future refactor
	// removes the only context-using method on the adapter.
	_ = context.Background
}
