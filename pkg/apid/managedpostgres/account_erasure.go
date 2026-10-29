package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	domain "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrAccountErasurePending keeps an account row through another grace tick
// while its managed PostgreSQL databases are not yet deleted tombstones.
var ErrAccountErasurePending = errors.New("managed postgres: account databases are not deleted yet")

// ErasureDatabases is the slice of domain.Service the erasure needs.
type ErasureDatabases interface {
	AccountErasure(context.Context, string) (domain.AccountErasure, error)
	Delete(context.Context, string, string) (domain.Database, error)
}

// ErasureBindings is the slice of domain.BindingService the erasure needs.
type ErasureBindings interface {
	List(context.Context, string, string) ([]domain.Binding, error)
	Delete(context.Context, string, string) (domain.Binding, error)
}

// AccountErasure is the grace sweep's managed PostgreSQL step
// (ADR-20261024150500). When an account's pending deletion includes its
// databases, it deletes their bindings and then the databases through the
// same lifecycle a customer's delete uses. It reports ErrAccountErasurePending
// until every database is a deleted tombstone, so the account row is deleted
// last. apid is the only caller: it owns the managed_postgres_* rows.
type AccountErasure struct {
	Databases ErasureDatabases
	Bindings  ErasureBindings
	// Audit records each deletion; the sweep is the actor.
	Audit func(ctx context.Context, kind string, accountID *string, data map[string]any)
	Log   *slog.Logger
}

// BeforeAccountDelete matches grace.Params.BeforeAccountDelete.
func (e AccountErasure) BeforeAccountDelete(ctx context.Context, acct state.Account) error {
	if e.Databases == nil {
		return nil
	}
	erasure, err := e.Databases.AccountErasure(ctx, acct.ID)
	if err != nil {
		return fmt.Errorf("managed postgres: read account erasure: %w", err)
	}
	if len(erasure.Databases) == 0 {
		return nil
	}
	if !erasure.Included {
		// Only a bypassed guard gets here; never delete data nobody chose to.
		return fmt.Errorf("%w: %d live databases and the deletion does not include them", ErrAccountErasurePending, len(erasure.Databases))
	}
	for _, database := range erasure.Databases {
		e.erase(ctx, acct.ID, database)
	}
	after, err := e.Databases.AccountErasure(ctx, acct.ID)
	if err != nil {
		return fmt.Errorf("managed postgres: read account erasure: %w", err)
	}
	if len(after.Databases) > 0 {
		return fmt.Errorf("%w: %d databases remain", ErrAccountErasurePending, len(after.Databases))
	}
	return nil
}

// erase deletes one database's bindings, then the database. A failure leaves
// it for the next tick: a binding still revoking, a restore descendant not yet
// gone, or a provider deletion still running.
func (e AccountErasure) erase(ctx context.Context, accountID string, database domain.Database) {
	if e.Bindings != nil {
		bindings, err := e.Bindings.List(ctx, accountID, database.ID)
		if err != nil {
			e.log().Warn("managed postgres erasure: list bindings", "account", accountID, "database", database.ID, "err", err)
			return
		}
		for _, binding := range bindings {
			if binding.State == domain.BindingStateDeleted {
				continue
			}
			deleted, err := e.Bindings.Delete(ctx, binding.AccountID, binding.ID)
			if err != nil {
				e.log().Warn("managed postgres erasure: delete binding", "account", accountID, "binding", binding.ID, "err", err)
				return
			}
			e.audit(ctx, "managed_postgres.binding.deleted", binding.AccountID, map[string]any{
				"binding_id": deleted.ID, "database_id": deleted.DatabaseID, "app_id": deleted.AppID, "actor": "grace-sweep",
			})
		}
	}
	deleted, err := e.Databases.Delete(ctx, accountID, database.ID)
	if err != nil {
		e.log().Warn("managed postgres erasure: delete database", "account", accountID, "database", database.ID, "err", err)
		return
	}
	e.audit(ctx, "managed_postgres.database.deleted", accountID, map[string]any{
		"database_id": deleted.ID, "state": deleted.State, "actor": "grace-sweep",
	})
}

func (e AccountErasure) audit(ctx context.Context, kind, accountID string, data map[string]any) {
	if e.Audit != nil {
		e.Audit(ctx, kind, &accountID, data)
	}
}

func (e AccountErasure) log() *slog.Logger {
	if e.Log == nil {
		return slog.Default()
	}
	return e.Log
}
