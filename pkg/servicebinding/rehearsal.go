// adr: 20261017201005
package servicebinding

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// AuthorizeRehearsalService requires retained policy and active namespace
// membership to agree. Pruning a
// namespace, mutating preview metadata or using an alternate resolver cannot
// convert a contained caller into a production caller.
func AuthorizeRehearsalService(ctx context.Context, store AuthorizationStore, caller, target state.App) (string, error) {
	run, err := store.ScenarioRehearsalNetworkRun(ctx, caller.ID)
	if err != nil || run == "" {
		return "", err
	}
	otherRun, err := store.ScenarioRehearsalNetworkRun(ctx, target.ID)
	if err != nil {
		return "", err
	}
	if run != otherRun {
		return "", gateway.ErrServiceProxyDenied
	}
	for _, app := range []state.App{caller, target} {
		member, err := store.ScenarioTestMemberByApp(ctx, app.ID)
		if err != nil || member.RunID != run || member.AccountID != caller.AccountID || app.PreviewExpiresAt == nil ||
			!app.PreviewExpiresAt.After(time.Now()) || app.PreviewPrState != state.PreviewPrStateOpen {
			return "", gateway.ErrServiceProxyDenied
		}
	}
	return run, nil
}
