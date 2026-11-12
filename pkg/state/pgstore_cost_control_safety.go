package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ReserveBillingUsageIntent(ctx context.Context, provider, account string, meter BillingMeter, hour time.Time, quantity int64) (int64, error) {
	id, err := parsePgUUID(account)
	if err != nil || provider == "" || (meter != BillingMeterCompute && meter != BillingMeterEgress) || hour.IsZero() || quantity < 0 {
		return 0, ErrInvalidArgument
	}
	return sqlc.New().ReserveBillingUsageIntent(ctx, s.pool, sqlc.ReserveBillingUsageIntentParams{
		Provider: provider, AccountID: id, Meter: string(meter), WindowStart: financialTimestamp(hour.UTC().Truncate(time.Hour)), Quantity: quantity,
	})
}

func (s *PgStore) ClaimSpendCapNotice(ctx context.Context, now time.Time) (SpendCapNoticeDelivery, error) {
	token, _ := parsePgUUID(uuid.NewString())
	r, err := sqlc.New().ClaimSpendCapNotice(ctx, s.pool, sqlc.ClaimSpendCapNoticeParams{
		NowAt: financialTimestamp(now), LeaseToken: token, LeaseUntil: financialTimestamp(now.Add(api.FinancialBudgetAlertLease)),
	})
	if err != nil {
		return SpendCapNoticeDelivery{}, mapErr(err)
	}
	return SpendCapNoticeDelivery{ID: pgUUIDString(r.ID), LeaseToken: pgUUIDString(r.LeaseToken), SpendCapHold: SpendCapHold{
		AccountID: pgUUIDString(r.AccountID), Month: r.Month.Time.UTC(), CapCents: r.CapCents, ObservedCents: r.ObservedCents, ReachedAt: r.ReachedAt.Time.UTC(),
	}}, nil
}

func (s *PgStore) SettleSpendCapNotice(ctx context.Context, id, token string, now time.Time, sent bool) error {
	uid, e1 := parsePgUUID(id)
	lease, e2 := parsePgUUID(token)
	if e1 != nil || e2 != nil {
		return ErrInvalidArgument
	}
	p := sqlc.SettleSpendCapNoticeParams{ID: uid, LeaseToken: lease, NowAt: financialTimestamp(now), State: "pending", NextAttemptAt: financialTimestamp(now.Add(api.FinancialBudgetAlertRetryBase))}
	if sent {
		p.State = "delivered"
		p.DeliveredAt = financialTimestamp(now)
	}
	_, err := sqlc.New().SettleSpendCapNotice(ctx, s.pool, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}

func (s *PgStore) BillingPlanActivationHead(ctx context.Context) (int64, error) {
	return sqlc.New().BillingPlanActivationHead(ctx, s.pool)
}

func (s *PgStore) ListBillingPlanActivations(ctx context.Context, subject string, start, through time.Time) ([]BillingPlanActivation, error) {
	id, err := parsePgUUID(subject)
	if err != nil || start.IsZero() || through.Before(start) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListBillingPlanActivations(ctx, s.pool, sqlc.ListBillingPlanActivationsParams{SubjectID: id, PeriodStart: financialTimestamp(start), ThroughAt: financialTimestamp(through)})
	if err != nil {
		return nil, err
	}
	out := make([]BillingPlanActivation, 0, len(rows))
	for _, r := range rows {
		out = append(out, BillingPlanActivation{Sequence: r.Sequence, From: api.Plan(r.FromPlan), To: api.Plan(r.ToPlan), At: r.EffectiveAt.Time.UTC()})
	}
	return out, nil
}

func (s *PgStore) FinancialBudgetJobs(ctx context.Context, account string, limit int) ([]Job, error) {
	id, err := parsePgUUID(account)
	if err != nil || limit < 1 || limit > api.FinancialAllocationMax+1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialBudgetJobs(ctx, s.pool, sqlc.FinancialBudgetJobsParams{AccountID: id, PageSize: int32(limit)}) //nolint:gosec // bounded above
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, Job{ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), Name: r.Name, Status: r.Status})
	}
	return out, nil
}
