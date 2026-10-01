// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TenantHostnameChallengeVerifier publishes only the challenge observed by a
// DNS probe. A deleted and recreated hostname cannot inherit that probe.
type TenantHostnameChallengeVerifier interface {
	MarkTenantHostnameVerifiedIfChallenge(context.Context, string, string) (bool, error)
}

func (s *PgStore) markTrafficTenantHostnameVerified(ctx context.Context, hostname, token string, challengeBound bool) (bool, error) {
	queries := sqlc.New()
	owner, err := queries.ReadTenantHostnameTrafficOwner(ctx, s.pool, sqlc.ReadTenantHostnameTrafficOwnerParams{
		Hostname: hostname, Token: token, ChallengeBound: challengeBound,
	})
	if errors.Is(err, pgx.ErrNoRows) && challengeBound {
		return false, nil
	}
	if err != nil {
		return false, mapErr(err)
	}
	tx, err := s.beginTrafficPolicyMutation(ctx, owner.AccountID)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	changed, err := queries.MarkTrafficTenantHostnameVerified(ctx, tx, sqlc.MarkTrafficTenantHostnameVerifiedParams{
		Hostname: hostname, HostnameID: owner.ID, SurfaceID: owner.SurfaceID, Token: token, ChallengeBound: challengeBound,
	})
	if err != nil {
		return false, mapErr(err)
	}
	if changed == 0 {
		if challengeBound {
			return false, nil
		}
		return false, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *PgStore) MarkTenantHostnameVerifiedIfChallenge(ctx context.Context, hostname, token string) (bool, error) {
	return s.markTrafficTenantHostnameVerified(ctx, hostname, token, true)
}

func (s *PgStore) activateTrafficTenantSurface(ctx context.Context, surface string) error {
	queries := sqlc.New()
	account, err := queries.ReadTenantSurfaceTrafficAccount(ctx, s.pool, uuidToPgtype(surface))
	if err != nil {
		return mapErr(err)
	}
	tx, err := s.beginTrafficPolicyMutation(ctx, account)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	changed, err := queries.ActivateTrafficTenantSurface(ctx, tx, sqlc.ActivateTrafficTenantSurfaceParams{SurfaceID: uuidToPgtype(surface), AccountID: account})
	if err != nil {
		return mapErr(err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *PgStore) activateTrafficPlatformTenant(ctx context.Context, account, tenant string) (PlatformTenant, error) {
	tx, err := s.beginTrafficPolicyMutation(ctx, uuidToPgtype(account))
	if err != nil {
		return PlatformTenant{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	data, err := sqlc.New().ActivateTrafficPlatformTenant(ctx, tx, sqlc.ActivateTrafficPlatformTenantParams{AccountID: uuidToPgtype(account), TenantID: uuidToPgtype(tenant)})
	if err != nil {
		return PlatformTenant{}, mapErr(err)
	}
	var result PlatformTenant
	if err := json.Unmarshal(data, &result); err != nil {
		return PlatformTenant{}, fmt.Errorf("state: decode activated platform tenant: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenant{}, err
	}
	return result, nil
}
