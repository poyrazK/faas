package ingressroute

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// RequireActiveOwner checks current resource policy, including company-owned
// apps whose creator's personal account no longer owns the runtime entitlement.
func RequireActiveOwner(ctx context.Context, reader state.AppOwnerReader, app state.App) error {
	owner, err := state.AppResourceAccount(ctx, reader, app)
	if err != nil {
		return fmt.Errorf("read raw ingress resource owner: %w", err)
	}
	if !owner.Active() {
		return errors.New("raw ingress resource owner is suspended, deleted or abuse-held")
	}
	return nil
}
