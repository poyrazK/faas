package conformance

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testNonUUIDInvocationIdentityIsUnowned reproduces production-us after
// rc.242: workflow steps run as synthetic invocations with IDs such as
// "workflow-<request-id>". resolveInvocationVersion only exempted ESM batches
// from the durable-identity lookups, so every step failed ("synth invoke
// resolve version: state: invalid argument") and every workflow run went
// dead with a 502. Durable sources must still reject a non-UUID identity.
func testNonUUIDInvocationIdentityIsUnowned(t *testing.T, fx *Fixture) {
	t.Helper()
	const id = "workflow-0123456789abcdef"
	inv := state.Invocation{
		ID: id, AppID: fx.App.ID, Source: state.InvocationSource("workflow"),
		Method: "POST", Path: "/a", Headers: []byte(`{"X-Faas-Workflow-Run-Id":"run-1"}`),
	}
	if _, _, err := state.ResolveInvocationVersion(fx.Ctx, fx.Store, inv); err != nil {
		t.Fatalf("ResolveInvocationVersion(workflow step) = %v, want the workflow step to resolve", err)
	}
	// ADR-590: only synthetic deliveries may carry a correlation identity.
	durable := inv
	durable.Source = state.InvocationAsyncInvoke
	if _, _, err := state.ResolveInvocationVersion(fx.Ctx, fx.Store, durable); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("durable invocation with a non-UUID identity = %v, want ErrInvalidArgument", err)
	}
}
