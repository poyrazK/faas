package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	quotasql "github.com/onebox-faas/faas/pkg/state/appquota/sqlc"
	ownersql "github.com/onebox-faas/faas/pkg/state/resourceownership/sqlc"
)

// resolveAppQuotaTx locks the persisted tenant's admission pool. Account locks
// still protect the legacy storage reference; the organization fence row
// serializes different creators. The org SHARE lock precedes its quota lock,
// matching the existing app-scope insert guard and serializing plan changes.
func resolveAppQuotaTx(ctx context.Context, tx pgx.Tx, app *App, limits api.Limits) (api.Limits, bool, error) {
	if app.ProjectID != "" {
		owner, err := ownersql.New().ProjectOwner(ctx, tx, mustPgUUID(app.ProjectID))
		if err != nil {
			return api.Limits{}, false, mapErr(err)
		}
		if pgUUIDString(owner.AccountID) != app.AccountID || app.OrgID != "" && app.OrgID != pgUUIDString(owner.OrgID) {
			return api.Limits{}, false, ErrConflict
		}
		app.OrgID = pgUUIDString(owner.OrgID)
	}
	q := quotasql.New()
	if app.OrgID != "" && !mustPgUUID(app.OrgID).Valid {
		return api.Limits{}, false, ErrInvalidArgument
	}
	owner, err := q.ResolveOwner(ctx, tx, quotasql.ResolveOwnerParams{
		OrgID: mustPgUUID(app.OrgID), AccountID: mustPgUUID(app.AccountID),
	})
	if errors.Is(err, pgx.ErrNoRows) && app.OrgID == "" {
		// Compatibility for unattributed internal fixtures. Production account
		// creation atomically provisions a personal organization.
		return limits, true, nil
	}
	if err != nil {
		return api.Limits{}, false, mapErr(err)
	}
	if owner.PersonalOrg && pgUUIDString(owner.PersonalOwnerAccountID) != app.AccountID {
		return api.Limits{}, false, ErrConflict
	}
	app.OrgID = pgUUIDString(owner.ID)
	if err := q.LockOwner(ctx, tx, owner.ID); err != nil {
		return api.Limits{}, false, fmt.Errorf("state: lock organization app quota: %w", err)
	}
	if !owner.PersonalOrg {
		if owner.DeletedPending || owner.Status != string(OrgStatusActive) {
			return api.Limits{}, false, ErrOrgAppAdmissionUnavailable
		}
		status, err := q.AccountStatus(ctx, tx, mustPgUUID(app.AccountID))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return api.Limits{}, false, mapErr(err)
		}
		if status == string(AccountDeletedPending) {
			return api.Limits{}, false, ErrOrgAppAdmissionUnavailable
		}
		var ok bool
		limits, ok = api.LimitsFor(api.Plan(owner.Plan))
		if !ok {
			return api.Limits{}, false, ErrInvalidArgument
		}
	}
	return limits, owner.PersonalOrg, nil
}

func countAppQuotaTx(ctx context.Context, tx pgx.Tx, app App, personal bool, kind QuotaErrorKind) (int, error) {
	count, err := quotasql.New().CountApps(ctx, tx, quotasql.CountAppsParams{
		OrgID: mustPgUUID(app.OrgID), AccountID: mustPgUUID(app.AccountID),
		Personal: personal, Kind: string(kind),
	})
	if err != nil {
		return 0, fmt.Errorf("state: count organization apps: %w", err)
	}
	return int(count), nil
}

func (s *PgStore) countPersonalAppQuota(ctx context.Context, accountID string, kind QuotaErrorKind) (int, error) {
	count, err := quotasql.New().CountPersonalApps(ctx, s.pool, quotasql.CountPersonalAppsParams{
		AccountID: mustPgUUID(accountID), Kind: string(kind),
	})
	if err != nil {
		return 0, fmt.Errorf("state: count personal application quota: %w", err)
	}
	return int(count), nil
}

func checkAppQuotaTx(ctx context.Context, tx pgx.Tx, app App, limits api.Limits) error {
	return checkAppQuotaDeltaTx(ctx, tx, app, limits, 1)
}

func checkAppQuotaDeltaTx(ctx context.Context, tx pgx.Tx, app App, limits api.Limits, delta int) error {
	limits, personal, err := resolveAppQuotaTx(ctx, tx, &app, limits)
	if err != nil {
		return err
	}
	return checkResolvedAppQuotaTx(ctx, tx, app, limits, personal, delta)
}

func checkResolvedAppQuotaTx(ctx context.Context, tx pgx.Tx, app App, limits api.Limits, personal bool, delta int) error {
	limit, kind := appQuotaLimit(app, limits)
	observed, err := countAppQuotaTx(ctx, tx, app, personal, kind)
	if err != nil {
		return err
	}
	if observed+delta > limit {
		if delta != 1 {
			observed += delta
		}
		return &QuotaError{Kind: kind, Plan: limits.Plan, Limit: limit, Observed: observed}
	}
	return nil
}

func guardLegacySharedAppAccountTx(ctx context.Context, tx pgx.Tx, accountID string) error {
	q := quotasql.New()
	if _, err := q.LockAccount(ctx, tx, mustPgUUID(accountID)); err != nil {
		return mapErr(err)
	}
	blocked, err := ownersql.New().ErasureBlocked(ctx, tx, mustPgUUID(accountID))
	if err != nil {
		return fmt.Errorf("state: check shared app ownership before account deletion: %w", err)
	}
	if blocked {
		return ErrOrgAppOwnershipPending
	}
	return nil
}

func (s *PgStore) markAccountDeletionPending(ctx context.Context, accountID string, deletion accountDeletion) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOrgAccess(ctx, tx)
	if err := guardLegacySharedAppAccountTx(ctx, tx, accountID); err != nil {
		return err
	}
	changed, err := quotasql.New().MarkDeletion(ctx, tx, quotasql.MarkDeletionParams{ID: mustPgUUID(accountID), IncludeManagedPostgres: deletion.managedPostgres})
	if err != nil {
		return mapErr(err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
