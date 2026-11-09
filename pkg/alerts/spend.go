package alerts

import "context"

// SpendCents reads an account's month-to-date spend beyond its plan, in
// cents.
type SpendCents func(ctx context.Context, accountID string) (int64, error)

// SetSpend sets how the account_spend_eur metric reads an account's month
// to date (ADR-20261109153000): meterd passes the billing page's figure,
// compute plus egress plus object storage. nil, the default, reads the
// store's compute overage alone. A nil evaluator ignores it.
func (e *Evaluator) SetSpend(spend SpendCents) {
	if e == nil {
		return
	}
	e.spend = spend
}

// monthSpendCents reads the account's month-to-date spend.
func (e *Evaluator) monthSpendCents(ctx context.Context, accountID string) (int64, error) {
	if e.spend != nil {
		return e.spend(ctx, accountID)
	}
	return e.store.MTDSpendEurCents(ctx, accountID)
}
