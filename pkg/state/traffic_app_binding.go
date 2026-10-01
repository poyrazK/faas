// adr: 375
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TrafficPolicyBindingError marks analysis that can include a foreign owner.
// API clients must not expose its witness, policy scope, or exact count.
type TrafficPolicyBindingError struct{ Cause error }

func (e *TrafficPolicyBindingError) Error() string {
	return fmt.Sprintf("state: app binding traffic policy: %v", e.Cause)
}
func (e *TrafficPolicyBindingError) Unwrap() error { return e.Cause }

func appTrafficBindingError(err error) error {
	var aggregate *TrafficPolicyAggregateError
	var analysis *TrafficPolicyAnalysisError
	var projection *TrafficPolicyProjectionError
	if errors.As(err, &aggregate) || errors.As(err, &analysis) || errors.As(err, &projection) {
		return &TrafficPolicyBindingError{Cause: err}
	}
	return err
}

type appTrafficBindingTx struct{ pgx.Tx }

func (tx *appTrafficBindingTx) Commit(ctx context.Context) error {
	return appTrafficBindingError(tx.Tx.Commit(ctx))
}

func (s *PgStore) beginAccountAppTrafficMutation(ctx context.Context, account, appID string) (pgx.Tx, error) {
	tx, err := s.beginTrafficBinding(ctx, account, nil, appID)
	if err != nil {
		return nil, appTrafficBindingError(err)
	}
	if appID != "" {
		err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
			owner, err := sqlc.New().LockTrafficAppAccount(bounded, tx, uuidToPgtype(appID))
			if err != nil {
				return mapErr(err)
			}
			if owner != uuidToPgtype(account) {
				return ErrConflict
			}
			return nil
		})
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return nil, appTrafficBindingError(err)
		}
	}
	return &appTrafficBindingTx{Tx: tx}, nil
}

func cancelAppInvocationsTx(ctx context.Context, tx pgx.Tx, appID string) error {
	return sqlc.New().CancelTrafficAppInvocations(ctx, tx, uuidToPgtype(appID))
}

func (m *MemStore) cancelAppInvocationsLocked(appID string, now time.Time) {
	for id, inv := range m.invocations {
		if inv.AppID != appID || (inv.State != InvocationPending && inv.State != InvocationDispatching) || inv.State == InvocationDispatching && inv.WorkPolicyName != "" {
			continue
		}
		reserved := inv.QuotaReserved
		inv.State, inv.QuotaReserved = InvocationCancelled, false
		if inv.CompletedAt == nil {
			inv.CompletedAt = &now
		}
		m.invocations[id] = inv
		if reserved {
			m.decrementAccountAsyncInflightLocked(inv.AccountID)
		}
	}
}
