package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newServiceAliasAllowed is shared by the guest DNS and HTTP proxy paths.
// DNS controls discoverability; this HTTP check remains authoritative when a
// guest bypasses DNS and sends a .internal Host header to the bridge directly.
func newServiceAliasAllowed(store state.Store) gateway.ServiceAliasAllowed {
	return func(ctx context.Context, callerAppID, service string) (bool, error) {
		if !isAppID(callerAppID) || store == nil {
			return false, nil
		}
		caller, err := store.AppByID(ctx, callerAppID)
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("load service alias caller: %w", err)
		}
		if caller.AccountID == "" || caller.Status == state.AppDeleted {
			return false, nil
		}
		for _, binding := range caller.Manifest.ServiceBindings {
			if strings.EqualFold(strings.TrimSpace(binding.Service), service) {
				return true, nil
			}
		}
		return false, nil
	}
}
