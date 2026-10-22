package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (t *pgFinancialComputeTxn) egressOwner(node, incarnation string) (bool, error) {
	n, _ := parsePgUUID(node)
	i, _ := parsePgUUID(incarnation)
	return t.q.FinancialEgressOwnerValid(t.ctx, t.db, sqlc.FinancialEgressOwnerValidParams{NodeID: n, Incarnation: i})
}
func (t *pgFinancialComputeTxn) egressPrices(month time.Time) ([]FinancialPriceSnapshot, error) {
	rows, err := t.q.FinancialPriceSnapshotsList(t.ctx, t.db, sqlc.FinancialPriceSnapshotsListParams{AccountID: t.account, PeriodStart: pgTime(month)})
	if err != nil {
		return nil, err
	}
	prices := make([]FinancialPriceSnapshot, 0, len(rows))
	for _, row := range rows {
		var p financial.Price
		if err := json.Unmarshal(row.Price, &p); err != nil {
			return nil, err
		}
		prices = append(prices, FinancialPriceSnapshot{Price: p, PeriodStart: row.PeriodStart.Time, EffectiveFrom: row.EffectiveFrom.Time})
	}
	return prices, nil
}
func (t *pgFinancialComputeTxn) egressReady() error {
	ready, err := t.accountReady()
	if err != nil {
		return err
	}
	if !ready {
		return financial.ErrComputeUnavailable
	}
	owners, err := t.q.FinancialEgressOwnersReady(t.ctx, t.db, 3*api.FinancialComputeOwnerHeartbeat.Seconds())
	if err != nil {
		return err
	}
	if !owners {
		return financial.ErrComputeUnavailable
	}
	sampling, err := t.q.FinancialEgressSamplingReady(t.ctx, t.db, sqlc.FinancialEgressSamplingReadyParams{AccountID: t.account, PeriodStart: pgTime(financial.ComputeMonth(t.now))})
	if err != nil {
		return err
	}
	if !sampling.Valid || !sampling.Bool {
		return financial.ErrComputeUnavailable
	}
	return nil
}

func (t *pgFinancialComputeTxn) initializeEgress() error {
	if err := t.egressReady(); err != nil {
		return err
	}
	b, err := t.book("", financial.ComputeMonth(t.now))
	if err != nil {
		return err
	}
	prices, err := t.egressPrices(b.PeriodStart)
	if err != nil {
		return err
	}
	b.Egress, err = computeEgressTerms(prices, b.PeriodStart, t.now, b.Egress)
	if err != nil {
		return err
	}
	baseline, err := t.q.FinancialEgressBaseline(t.ctx, t.db, sqlc.FinancialEgressBaselineParams{AccountID: t.account, PeriodStart: pgTime(b.PeriodStart), PeriodEnd: pgTime(b.PeriodEnd), UpperRate: b.Egress.RateMillicents, UpperQuantity: b.Egress.RateQuantity})
	if err != nil {
		return err
	}
	if baseline.Incomplete {
		return financial.ErrComputeUnavailable
	}
	quantity, err := strconv.ParseInt(baseline.Quantity, 10, 64)
	if err != nil || quantity < 0 || quantity > api.FinancialComputeQuantityMax {
		return financial.ErrOverflow
	}
	b.Egress.QuantityCommitted = max(b.Egress.QuantityCommitted, quantity)
	if err = computeEgressReprice(&b.Egress); err != nil {
		return err
	}
	return t.putBook(b)
}

func (s *PgStore) FinancialEgressReady(ctx context.Context, account string) (bool, error) {
	a, err := parsePgUUID(account)
	if err != nil {
		return false, ErrInvalidArgument
	}
	q := sqlc.New()
	clock, err := q.FinancialComputeClock(ctx, s.pool)
	if err != nil {
		return false, err
	}
	t := &pgFinancialComputeTxn{ctx: ctx, db: s.pool, q: q, account: a, now: clock.Time}
	err = t.egressReady()
	if err == nil {
		prices, readErr := t.egressPrices(financial.ComputeMonth(t.now))
		if readErr != nil {
			return false, readErr
		}
		_, err = computeEgressTerms(prices, financial.ComputeMonth(t.now), t.now, financial.EgressBook{})
	}
	if errors.Is(err, financial.ErrComputeUnavailable) {
		return false, nil
	}
	return err == nil, err
}
