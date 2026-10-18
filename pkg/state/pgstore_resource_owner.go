package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	ownersql "github.com/onebox-faas/faas/pkg/state/resourceownership/sqlc"
)

func resourceTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	out := value.Time
	return &out
}

func orgResourceAccount(row ownersql.Org) Account {
	status := AccountStatus(row.Status)
	if row.DeletedPending {
		status = AccountDeletedPending
	}
	id := pgUUIDString(row.ID)
	return Account{ID: id, ResourceOrgID: id, ResourceOwnershipEnabled: row.ResourceOwnershipEnabled, Plan: api.Plan(row.Plan), Status: status,
		Email: row.BillingEmail, ProviderCustomerID: row.ProviderCustomerID.String,
		StripeSubscriptionItem: row.StripeSubscriptionItem.String, CreatedAt: row.CreatedAt.Time,
		PastDueAt: resourceTime(row.PastDueAt), LastQuotaWarningAt: resourceTime(row.LastQuotaWarningAt),
		AbuseHoldAt: resourceTime(row.AbuseHoldAt), AbuseHoldReason: row.AbuseHoldReason.String,
		BusinessName: row.BusinessName, BillingAddress: row.BillingAddress, TaxID: row.TaxID}
}

func (s *PgStore) OrgResourceAccount(ctx context.Context, id string) (Account, error) {
	row, err := ownersql.New().OrgAccount(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return Account{}, mapErr(err)
	}
	return orgResourceAccount(row), nil
}

func (s *PgStore) ListOrgResourceAccounts(ctx context.Context) ([]Account, error) {
	rows, err := ownersql.New().ListOrgAccounts(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, orgResourceAccount(row))
	}
	return out, nil
}

func (s *PgStore) UpdateOrgBillingEmail(ctx context.Context, id, email string) error {
	n, err := ownersql.New().UpdateBillingEmail(ctx, s.pool, ownersql.UpdateBillingEmailParams{ID: mustPgUUID(id), BillingEmail: email})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) orgQuotaWarning(ctx context.Context, id string, day time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackOrgAccess(ctx, tx)
	q := ownersql.New()
	key := mustPgUUID(id)
	stamp := pgtype.Timestamptz{Time: day.UTC().Truncate(24 * time.Hour), Valid: true}
	already, err := q.ReadQuotaWarning(ctx, tx, ownersql.ReadQuotaWarningParams{ID: key, LastQuotaWarningAt: stamp})
	if err != nil {
		return false, mapErr(err)
	}
	if err = q.StampQuotaWarning(ctx, tx, ownersql.StampQuotaWarningParams{ID: key, LastQuotaWarningAt: stamp}); err != nil {
		return false, err
	}
	return already, tx.Commit(ctx)
}

// isOrgBillingSubject checks a financial subject, never an authenticator.
func (s *PgStore) isOrgBillingSubject(ctx context.Context, id string) (bool, error) {
	_, err := s.OrgResourceAccount(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *PgStore) updateOrgResourcePlan(ctx context.Context, id string, plan api.Plan) error {
	if !validResourcePlan(plan) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOrgAccess(ctx, tx)
	q := ownersql.New()
	key := mustPgUUID(id)
	if _, err = q.LockOrg(ctx, tx, key); err != nil {
		return mapErr(err)
	}
	if plan == api.PlanFree {
		if err = q.DisableStreaming(ctx, tx, key); err != nil {
			return err
		}
	}
	if _, err = q.UpdatePlan(ctx, tx, ownersql.UpdatePlanParams{ID: key, Plan: string(plan)}); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) updateOrgResourceProvider(ctx context.Context, id, customer, subscription string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOrgAccess(ctx, tx)
	q := ownersql.New()
	key := mustPgUUID(id)
	if _, err = q.LockOrg(ctx, tx, key); err != nil {
		return mapErr(err)
	}
	row, err := q.OrgAccount(ctx, tx, key)
	if err != nil {
		return mapErr(err)
	}
	if customer != "" {
		existing, lookupErr := q.OwnedCustomerSubscription(ctx, tx, ownersql.OwnedCustomerSubscriptionParams{AccountID: key, Provider: billingProviderForCustomerID(customer), CustomerID: customer})
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			return mapErr(lookupErr)
		}
		_, err = q.UpdateCustomer(ctx, tx, ownersql.UpdateCustomerParams{ID: key, ProviderCustomerID: pgtype.Text{String: customer, Valid: true}, StripeSubscriptionItem: pgtype.Text{String: existing, Valid: existing != ""}})
	} else {
		customer = row.ProviderCustomerID.String
		_, err = q.UpdateSubscription(ctx, tx, ownersql.UpdateSubscriptionParams{ID: key, StripeSubscriptionItem: pgtype.Text{String: subscription, Valid: subscription != ""}})
	}
	if err != nil {
		return mapErr(err)
	}
	if customer != "" {
		if err = q.SaveBillingIdentity(ctx, tx, ownersql.SaveBillingIdentityParams{ID: key, Provider: billingProviderForCustomerID(customer)}); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit(ctx)
}
