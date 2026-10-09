package runtimeupgrade

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// DrainControls is a private read seam; no customer route or cleanup action.
type DrainControls struct {
	Store state.RuntimeUpgradeDrainVerificationStore
}

func (c DrainControls) Verify(ctx context.Context, accountID, operationID string, sessions []string) (state.RuntimeUpgradeDrainVerification, error) {
	if c.Store == nil {
		return state.RuntimeUpgradeDrainVerification{}, state.ErrInvalidArgument
	}
	out, err := c.Store.VerifyRuntimeUpgradeDrain(ctx, accountID, operationID, sessions)
	if err != nil {
		return out, fmt.Errorf("verify runtime forwarding drain: %w", err)
	}
	return out, nil
}
