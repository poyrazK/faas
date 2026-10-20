package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestValidateOperationJobAuthorityRejectsInt32Overflow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation int
		attempt    int
	}{
		{name: "generation", generation: 1 << 31, attempt: 1},
		{name: "attempt", generation: 1, attempt: 1 << 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := state.Operation{}
			op.Generation = tc.generation
			authority := state.JobOperationAuthority{Generation: tc.generation, Attempt: tc.attempt}
			if err := state.ValidateOperationJobAuthority(op, state.JobTask{}, authority, time.Now()); !errors.Is(err, state.ErrOperationStaleAttempt) {
				t.Fatalf("out-of-range authority error = %v", err)
			}
		})
	}
}
