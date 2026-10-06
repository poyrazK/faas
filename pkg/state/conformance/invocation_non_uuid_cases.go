package conformance

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testNonUUIDInvocationIdentityIsUnowned reproduces production-us after
// rc.242: workflow steps run as synthetic invocations with IDs such as
// "workflow-<request-id>". PgStore's ledger lookups rejected that identity
// with ErrInvalidArgument while MemStore reported ErrNotFound, so on Postgres
// resolveInvocationVersion failed every step ("synth invoke resolve version:
// state: invalid argument") and every workflow run went dead with a 502.
func testNonUUIDInvocationIdentityIsUnowned(t *testing.T, fx *Fixture) {
	t.Helper()
	const id = "workflow-0123456789abcdef"
	if reader, ok := fx.Store.(state.InvocationEnvironmentOwnerReader); ok {
		if _, err := reader.InvocationEnvironmentID(fx.Ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("InvocationEnvironmentID(%q) = %v, want ErrNotFound", id, err)
		}
	}
	if reader, ok := fx.Store.(state.InvocationWorkEnvironmentAdmissionReader); ok {
		if _, err := reader.InvocationWorkEnvironmentAdmission(fx.Ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("InvocationWorkEnvironmentAdmission(%q) = %v, want ErrNotFound", id, err)
		}
	}
	inv := state.Invocation{
		ID: id, AppID: fx.App.ID, Source: state.InvocationSource("workflow"),
		Method: "POST", Path: "/a", Headers: []byte(`{"X-Faas-Workflow-Run-Id":"run-1"}`),
	}
	if _, _, err := state.ResolveInvocationVersion(fx.Ctx, fx.Store, inv); err != nil {
		t.Fatalf("ResolveInvocationVersion(workflow step) = %v, want the workflow step to resolve", err)
	}
}
