package builderd

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRetryStateMutation_RetriesTransientFailure(t *testing.T) {
	attempts := 0
	if err := retryStateMutation(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary database failure")
		}
		return nil
	}); err != nil {
		t.Fatalf("retryStateMutation returned error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryStateMutation_DoesNotRetryStateDecision(t *testing.T) {
	attempts := 0
	if err := retryStateMutation(context.Background(), func() error {
		attempts++
		return errors.Join(errors.New("claim is stale"), state.ErrNotFound)
	}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
