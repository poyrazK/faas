package apidserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// cleanupPRPreviewPostgresBindings removes the managed PostgreSQL bindings of
// a PR preview being torn down. Previews can no longer be bound, but a binding
// made before ADR-20261105090226 kept the database's credentials attached to
// a deleted preview. Only the binding goes; the database is the customer's.
func (s *server) cleanupPRPreviewPostgresBindings(ctx context.Context, app state.App) error {
	if !state.IsPRPreviewApp(app) || s.managedPostgresBindings == nil {
		return nil
	}
	bindings, err := s.managedPostgresBindings.ListForApp(ctx, app.AccountID, app.ID, "")
	if errors.Is(err, managedpostgres.ErrUnsupported) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list PR preview postgres bindings: %w", err)
	}
	for _, binding := range bindings {
		if binding.State == string(managedpostgres.BindingStateDeleting) {
			continue
		}
		if _, err := s.managedPostgresBindings.Delete(ctx, app.AccountID, binding.BindingID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return fmt.Errorf("delete PR preview postgres binding: %w", err)
		}
	}
	return nil
}
