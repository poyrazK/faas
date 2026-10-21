package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrProductTrialUnavailable refuses changing a customer's trial that is not
// running (ADR-20261021195901).
var ErrProductTrialUnavailable = errors.New("state: product trial is not running")

// ProductTrialStore moves the end of a customer's running trial
// (ADR-20261021195901).
type ProductTrialStore interface {
	SetProductCustomerTrialEnd(ctx context.Context, account, app, tenant string, end time.Time) (api.ProductCustomer, error)
}

// validateProductTrialEnd accepts a running trial's new end no earlier than
// the next UTC minute and no more than the longest trial after its start.
func validateProductTrialEnd(c api.ProductCustomer, end, now time.Time) error {
	if c.TrialStartsAt == nil || c.TrialEndsAt == nil || !c.TrialEndsAt.After(now) {
		return ErrProductTrialUnavailable
	}
	next := now.UTC().Truncate(time.Minute).Add(time.Minute)
	if end.Before(next) || end.After(c.TrialStartsAt.AddDate(0, 0, api.ProductControlMaxTrialDays)) {
		return fmt.Errorf("%w: a trial ends between the next minute and %d days after it started", ErrInvalidArgument, api.ProductControlMaxTrialDays)
	}
	return nil
}

func (m *MemStore) SetProductCustomerTrialEnd(_ context.Context, account, app, tenant string, end time.Time) (api.ProductCustomer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[app]
	if !ok || a.AccountID != account || a.Status == AppDeleted {
		return api.ProductCustomer{}, ErrNotFound
	}
	d := m.productLocked()
	customer, ok := d.customers[app+"/"+tenant]
	if !ok {
		return api.ProductCustomer{}, ErrNotFound
	}
	if err := validateProductTrialEnd(customer, end, time.Now()); err != nil {
		return api.ProductCustomer{}, err
	}
	end = end.UTC()
	customer.TrialEndsAt = &end
	d.customers[app+"/"+tenant] = customer
	return customer, nil
}

func (s *PgStore) SetProductCustomerTrialEnd(ctx context.Context, account, app, tenant string, end time.Time) (api.ProductCustomer, error) {
	a, err := operationUUID(account)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	b, err := operationUUID(app)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	c, err := operationUUID(tenant)
	if err != nil {
		return api.ProductCustomer{}, err
	}
	var out api.ProductCustomer
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		row, err := q.ProductControlCustomerForUpdate(ctx, tx, sqlc.ProductControlCustomerForUpdateParams{AccountID: a, AppID: b, TenantID: c})
		if err != nil {
			return mapErr(err)
		}
		customer := withProductTrial(api.ProductCustomer{TenantID: tenant, Revision: row.Revision, Plan: row.Plan, Suspended: row.Suspended,
			AuthOrganizationID: pgUUIDString(row.AuthOrganizationID)}, row.TrialStartsAt, row.TrialEndsAt)
		if err := validateProductTrialEnd(customer, end, time.Now()); err != nil {
			return err
		}
		if err := q.ProductControlSetTrialEnd(ctx, tx, sqlc.ProductControlSetTrialEndParams{AccountID: a, AppID: b, TenantID: c,
			TrialEndsAt: pgtype.Timestamptz{Time: end.UTC(), Valid: true}}); err != nil {
			return err
		}
		ended := end.UTC()
		customer.TrialEndsAt = &ended
		out = customer
		return nil
	})
	return out, err
}
