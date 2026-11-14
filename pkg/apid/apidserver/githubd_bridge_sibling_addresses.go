package apidserver

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/state"
)

// bridgeEnvStore is the configuration surface the bridge needs to import a
// push's sibling service addresses; the production store implements it, the
// narrow unit-test stubs do not and skip the import.
type bridgeEnvStore interface {
	projectsetup.EnvKeyReader
	CountAppEnv(ctx context.Context, accountID, appID string) (int, error)
	UpsertAppEnvInScope(ctx context.Context, accountID, appID, scope, key, value string) error
}

// importPushSiblingAddresses sets the sibling service addresses githubd read
// from a push's Compose file on the app, in the deployment's scope, before
// its build (ADR-20261105104500). A reviewed project deploy imports them the
// same way; a push imported nothing, so a service it added started with its
// sibling's URL unset. Only keys the scope has no variable or secret for are
// written, and only values that are Gregale service addresses: anything else
// githubd sends is dropped. It returns the keys written.
func (g *githubdBridge) importPushSiblingAddresses(ctx context.Context, resource state.Account, app state.App, scope string, addresses map[string]string) ([]string, error) {
	store, ok := g.store.(bridgeEnvStore)
	if !ok || len(addresses) == 0 {
		return nil, nil
	}
	if scope == "" {
		scope = state.DefaultEnvScope
	}
	values := make(map[string]string, len(addresses))
	for key, value := range addresses {
		if api.ValidateEnvKey(key) != nil || projectsetup.PlatformProvidedEnvKey(key) || !projectsetup.ValidSiblingAddress(value) {
			g.log.Warn("githubd bridge: dropped a sibling address that is not one", "app_id", app.ID, "key", key)
			continue
		}
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	unset, err := projectsetup.UnsetEnvKeys(ctx, store, app.AccountID, scope, &app, keys)
	if err != nil || len(unset) == 0 {
		return nil, err
	}
	limits, ok := api.LimitsFor(resource.Plan)
	if !ok {
		return nil, nil
	}
	count, err := store.CountAppEnv(ctx, app.AccountID, app.ID)
	if err != nil {
		return nil, err
	}
	if count+len(unset) > limits.EnvVarsMax {
		return nil, &siblingAddressLimitError{fmt.Sprintf("setting %s would put %s over the plan's %d variables; set them with gregale env set", strings.Join(unset, ", "), app.Slug, limits.EnvVarsMax)}
	}
	for _, key := range unset {
		if err := store.UpsertAppEnvInScope(ctx, app.AccountID, app.ID, scope, key, values[key]); err != nil {
			return nil, err
		}
	}
	return unset, nil
}

// siblingAddressLimitError is a plan limit the push cannot fix by retrying.
type siblingAddressLimitError struct{ detail string }

func (e *siblingAddressLimitError) Error() string { return e.detail }
