package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) GetProductCustomerWorkSummary(ctx context.Context, account, app, tenant string) (api.ProductCustomerWorkSummary, error) {
	if !validProductUUID(tenant) {
		return api.ProductCustomerWorkSummary{}, ErrInvalidArgument
	}
	if _, err := s.GetProductCustomer(ctx, account, app, tenant); err != nil {
		return api.ProductCustomerWorkSummary{}, err
	}
	r, err := sqlc.New().ProductCustomerWorkSummary(ctx, s.pool, sqlc.ProductCustomerWorkSummaryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), TenantID: mustPgUUID(tenant)})
	if err != nil {
		return api.ProductCustomerWorkSummary{}, err
	}
	return api.ProductCustomerWorkSummary{Unresolved: r.Unresolved, NeedsReview: r.NeedsReview, ReviewPeriod: r.ReviewPeriod}, nil
}
