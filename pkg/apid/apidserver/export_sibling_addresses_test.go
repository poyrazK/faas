package apidserver

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/state"
)

// ImportPushSiblingAddresses runs the githubd bridge's sibling address import
// against store, for apidserver_test (ADR-20261105104500).
func ImportPushSiblingAddresses(ctx context.Context, store *state.MemStore, resource state.Account, app state.App, scope string, addresses map[string]string) ([]string, error) {
	g := &githubdBridge{store: store, log: slog.New(slog.DiscardHandler)}
	return g.importPushSiblingAddresses(ctx, resource, app, scope, addresses)
}
