package environmentgitops

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Verification failures are logged by step and sentinel class only: the error
// text can carry customer data.
func TestRuntimeVerificationFailureNamesStepAndClass(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		step, class string
	}{
		{"named conflict", &RuntimeVerificationError{Step: "serving", Err: fmt.Errorf("receipt for repo/secret: %w", state.ErrConflict)}, "serving", "conflict"},
		{"named not found", &RuntimeVerificationError{Step: "graph_preparation", Err: state.ErrNotFound}, "graph_preparation", "not_found"},
		{"wrapped twice", fmt.Errorf("outer: %w", &RuntimeVerificationError{Step: "activation", Err: state.ErrInvalidStateTransition}), "activation", "invalid_state_transition"},
		{"deadline", &RuntimeVerificationError{Step: "candidates", Err: context.DeadlineExceeded}, "candidates", "context"},
		{"unnamed internal", errors.New("customer text"), "unknown", "internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step, class := runtimeVerificationFailure(tc.err)
			if step != tc.step || class != tc.class {
				t.Fatalf("got %s/%s, want %s/%s", step, class, tc.step, tc.class)
			}
		})
	}
}
