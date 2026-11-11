package servicebinding

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// PreviewSetPeerStore reads a PR preview's set peers and their rows.
type PreviewSetPeerStore interface {
	AppByID(context.Context, string) (state.App, error)
	state.PRPreviewSetPeerReader
}

// StandalonePRPreview reports a PR preview of an app bound without a project.
// It has no project to scope its service names, so it calls only the other
// previews of its PR's preview set and never a production app
// (ADR-20261105090226).
func StandalonePRPreview(app state.App) bool {
	return app.PreviewOfSlug != "" && app.PreviewPrNumber > 0 && app.ProjectID == ""
}

// PreviewSetPeer resolves service, a production app's slug, to the preview of
// that app in caller's PR preview set. found is false when the set holds no
// live preview of it.
func PreviewSetPeer(ctx context.Context, store PreviewSetPeerStore, caller state.App, service string) (state.App, bool, error) {
	ids, err := store.PRPreviewSetPeerIDs(ctx, caller.ID)
	if err != nil {
		return state.App{}, false, fmt.Errorf("resolve preview set peers: %w", err)
	}
	if !slices.Contains(ids, caller.ID) {
		return state.App{}, false, nil
	}
	for _, id := range ids {
		if id == caller.ID {
			continue
		}
		peer, err := store.AppByID(ctx, id)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return state.App{}, false, fmt.Errorf("load preview set peer: %w", err)
		}
		if livePreviewSetPeer(caller, peer) && api.SameServiceName(peer.PreviewOfSlug, service) {
			return peer, true, nil
		}
	}
	return state.App{}, false, nil
}

// livePreviewSetPeer reports whether peer is a live preview of the same PR
// as caller, in the same account and without a project.
func livePreviewSetPeer(caller, peer state.App) bool {
	return peer.ID != "" && peer.ID != caller.ID && peer.Status != state.AppDeleted &&
		state.SameAppResourceOwner(caller, peer) && StandalonePRPreview(peer) &&
		peer.PreviewPrNumber == caller.PreviewPrNumber &&
		peer.PreviewPrState != state.PreviewPrStateStale &&
		peer.PreviewPrState != state.PreviewPrStateTearingDown &&
		peer.PreviewPrState != state.PreviewPrStateTornDown
}

// authorizeStandalonePreview applies ADR-20261105090226 to a call from a PR
// preview of an app bound without a project: a production target is refused,
// and a preview target must be a live peer in the caller's preview set.
func authorizeStandalonePreview(ctx context.Context, store AuthorizationStore, caller, target state.App) error {
	if target.PreviewOfSlug == "" {
		return gateway.ErrServiceProxyPreviewProductionDenied
	}
	peers, ok := store.(state.PRPreviewSetPeerReader)
	if !ok {
		return errors.New("preview set storage is unavailable")
	}
	ids, err := peers.PRPreviewSetPeerIDs(ctx, caller.ID)
	if err != nil {
		return fmt.Errorf("load preview set peers: %w", err)
	}
	if !slices.Contains(ids, caller.ID) || !slices.Contains(ids, target.ID) || !livePreviewSetPeer(caller, target) {
		return gateway.ErrServiceProxyDenied
	}
	return nil
}
