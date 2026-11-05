package apidserver

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/previewconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// pendingPRPreviewRemoval reads the pr-preview entry a delete is about to
// remove from a production app, so the previews that hold its copy can lose
// it too (ADR-20261103180000). ok is false for any other delete.
func (s *server) pendingPRPreviewRemoval(ctx context.Context, app state.App, scope, key string, secret bool) (previewconfig.Removal, bool) {
	if scope != api.PreviewEnvScope || app.PreviewOfSlug != "" {
		return previewconfig.Removal{}, false
	}
	removal, ok, err := previewconfig.PendingRemoval(ctx, s.store, app, key, secret)
	if err != nil {
		s.log.Warn("pr-preview delete: read entry", "app", app.ID, "err", err)
		return previewconfig.Removal{}, false
	}
	return removal, ok
}

// removeFromPRPreviews deletes removal from each PR preview of parent that
// still holds the copy a sync gave it, and invalidates their snapshots so a
// wake starts without it. The production app's delete has already committed,
// so a failure is logged and audited rather than undoing it.
func (s *server) removeFromPRPreviews(ctx context.Context, acct state.Account, parent state.App, removal previewconfig.Removal) {
	previews, err := previewconfig.PreviewsHolding(ctx, s.store, parent, removal)
	if err != nil {
		s.log.Error("pr-preview delete: list previews", "app", parent.ID, "err", err)
	}
	for _, preview := range previews {
		var deleteErr error
		if removal.Secret {
			_, deleteErr = s.store.DeleteAppSecretInScopeWithRevocation(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope, removal.Key)
		} else {
			deleteErr = s.store.DeleteAppEnvInScope(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope, removal.Key)
		}
		if deleteErr != nil && !errors.Is(deleteErr, state.ErrNotFound) {
			s.log.Error("pr-preview delete: remove from preview", "app", parent.ID, "preview", preview.ID, "err", deleteErr)
			s.audit.Emit(ctx, "preview_config.remove_failed", &acct.ID, map[string]any{
				"app_id": parent.ID, "preview_app_id": preview.ID, "name": removal.Key, "secret": removal.Secret,
			})
			continue
		}
		if _, err := s.invalidateAppSnapshots(ctx, preview.ID); err != nil {
			s.log.Error("pr-preview delete: invalidate preview snapshots", "preview", preview.ID, "err", err)
		}
		s.audit.Emit(ctx, "preview_config.removed", &acct.ID, map[string]any{
			"app_id": parent.ID, "preview_app_id": preview.ID, "name": removal.Key, "secret": removal.Secret,
		})
	}
}
