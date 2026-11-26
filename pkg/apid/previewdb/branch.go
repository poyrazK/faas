package previewdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Branch previews (ADR-20261113121342 amendment 2026-11-26): a pull
// request's environment branch gets the same empty preview database an app
// preview of that pull request would, owned by the project and the PR
// number, and binds it in the branch's scope.

// EnsureBranch returns the empty preview database owner's pull request
// branch gets on plan. It answers managedpostgres.ErrPreviewsOff when the
// plan gives no preview databases, so the branch goes without one as an app
// preview would, and ErrPending while the database is not ready yet (still
// provisioning, or a reset's purge still under way).
func (m Manager) EnsureBranch(ctx context.Context, plan api.Plan, accountID string, owner managedpostgres.PreviewOwner) (managedpostgres.Database, error) {
	limit := api.ManagedPostgresPreviewDatabasesMax(plan)
	if limit <= 0 {
		return managedpostgres.Database{}, managedpostgres.ErrPreviewsOff
	}
	database, err := m.Databases.EnsurePreviewDatabase(ctx, managedpostgres.PreviewDatabaseRequest{
		AccountID: accountID, Owner: owner, StorageLimitBytes: StorageLimitBytes(plan), Limit: limit,
	})
	if err != nil {
		return managedpostgres.Database{}, err
	}
	if database.State != managedpostgres.StateReady {
		return managedpostgres.Database{}, ErrPending
	}
	return database, nil
}

// BindBranch binds an app to a pull request branch's preview database in the
// branch's scope with the production binding's key and access. It answers
// ErrPending while the binding is provisioning or held by its reconciler.
func (m Manager) BindBranch(ctx context.Context, request managedpostgres.CreateBindingRequest) error {
	request.PreviewSetup = true
	binding, _, err := m.Bindings.CreateWithResult(ctx, request)
	if stillBinding(err, binding, request.DatabaseID, request.AppID) {
		return ErrPending
	}
	if err != nil {
		return err
	}
	if binding.DatabaseID != request.DatabaseID {
		return fmt.Errorf("previewdb: scope %s binds another database: %w", request.Scope, managedpostgres.ErrConflict)
	}
	if binding.State == managedpostgres.BindingStateProvisioning {
		return ErrPending
	}
	if binding.State != managedpostgres.BindingStateReady {
		return fmt.Errorf("previewdb: binding is %s: %w", binding.State, managedpostgres.ErrUnavailable)
	}
	return nil
}

// ReleaseBranch removes the bindings a pull request branch holds, in scope,
// to its PR environment's preview databases. It leaves the databases:
// PurgeUnbound removes them once the branch is gone.
func (m Manager) ReleaseBranch(ctx context.Context, accountID, environmentID string, prNumber int, scope string) error {
	databases, err := m.Databases.PreviewDatabases(ctx, accountID, environmentID, prNumber)
	if err != nil {
		return fmt.Errorf("previewdb: list preview databases: %w", err)
	}
	for _, database := range databases {
		bindings, err := m.Bindings.List(ctx, accountID, database.ID)
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return fmt.Errorf("previewdb: list bindings: %w", err)
		}
		for _, binding := range bindings {
			if binding.Scope != scope {
				continue
			}
			if _, err := m.Bindings.Delete(ctx, accountID, binding.ID); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
				return fmt.Errorf("previewdb: delete binding: %w", err)
			}
		}
	}
	return nil
}

// PurgeUnbound purges the PR environment's preview databases nothing binds
// any more (a binding that is neither ready nor provisioning is a leftover,
// not a user), so a reset branch starts from an empty database. A database
// whose purge conflicts (still provisioning, or its bindings still being
// revoked) is left: the next branch waits on it, and the sweep removes it.
func (m Manager) PurgeUnbound(ctx context.Context, accountID, environmentID string, prNumber int) error {
	databases, err := m.Databases.PreviewDatabases(ctx, accountID, environmentID, prNumber)
	if err != nil {
		return fmt.Errorf("previewdb: list preview databases: %w", err)
	}
	for _, database := range databases {
		bindings, err := m.Bindings.List(ctx, accountID, database.ID)
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
			return fmt.Errorf("previewdb: list bindings: %w", err)
		}
		live := false
		for _, binding := range bindings {
			live = live || copiedState(binding.State)
		}
		if live {
			continue
		}
		_, err = m.Purge(ctx, accountID, database.ID)
		if err != nil && !errors.Is(err, managedpostgres.ErrNotFound) && !errors.Is(err, managedpostgres.ErrConflict) {
			return fmt.Errorf("previewdb: purge preview database %s: %w", database.ID, err)
		}
	}
	return nil
}
