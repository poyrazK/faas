package projectsetup

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnvKeyReader reads the configuration already set in one environment scope.
type EnvKeyReader interface {
	ListAppEnvInScope(context.Context, string, string, string) ([]state.AppEnv, error)
	ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error)
}

// UnsetEnvKeys lists the runtime keys a workload names (its detected
// environment keys and declared required secrets) that will have no value in
// scope: neither configured on the existing app nor provided by the platform.
// Only key names are compared; no value enters the result.
func UnsetEnvKeys(ctx context.Context, store EnvKeyReader, accountID, scope string, app *state.App, keys ...[]string) ([]string, error) {
	wanted := map[string]bool{}
	for _, group := range keys {
		for _, key := range group {
			if !PlatformProvidedEnvKey(key) {
				wanted[key] = true
			}
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	if app != nil {
		variables, err := store.ListAppEnvInScope(ctx, accountID, app.ID, scope)
		if err != nil {
			return nil, fmt.Errorf("list configured variables: %w", err)
		}
		secrets, err := store.ListAppSecretsInScope(ctx, accountID, app.ID, scope)
		if err != nil {
			return nil, fmt.Errorf("list configured secrets: %w", err)
		}
		for _, variable := range variables {
			delete(wanted, variable.Key)
		}
		for _, secret := range secrets {
			delete(wanted, secret.Key)
		}
	}
	unset := make([]string, 0, len(wanted))
	for key := range wanted {
		unset = append(unset, key)
	}
	slices.Sort(unset)
	return unset, nil
}

// PlatformProvidedEnvKey reports keys Gregale sets itself: the listener
// port, platform settings, service URLs and identity headers.
func PlatformProvidedEnvKey(key string) bool {
	return key == "PORT" || strings.HasPrefix(key, "GREGALE_") || strings.HasPrefix(key, "FAAS_") || api.IsPlatformIdentityEnvKey(key)
}
