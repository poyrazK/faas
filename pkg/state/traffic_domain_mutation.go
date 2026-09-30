// adr: 375
package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) markTrafficDomainVerified(ctx context.Context, domain, token string, challengeBound bool) (bool, error) {
	queries := sqlc.New()
	owner, err := queries.ReadDomainTrafficVerificationOwner(ctx, s.pool, sqlc.ReadDomainTrafficVerificationOwnerParams{
		Domain: domain, Token: token, ChallengeBound: challengeBound,
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
	changed, err := queries.MarkTrafficDomainVerified(ctx, tx, sqlc.MarkTrafficDomainVerifiedParams{
		Domain: domain, AppID: owner.AppID, Token: token, ChallengeBound: challengeBound,
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

func (m *MemStore) checkMemTrafficDomainChangeLocked(ctx context.Context, domain CustomDomain) error {
	app, found := m.apps[domain.AppID]
	if !found {
		// Missing parents cannot route. Preserve the existing in-memory
		// domain behavior without inventing a serving scope.
		return ctx.Err()
	}
	return m.validateMemTrafficPolicyChangeLocked(ctx, app.AccountID, memTrafficPolicyChange{
		Domains: map[string]CustomDomain{domain.Domain: domain},
	})
}
