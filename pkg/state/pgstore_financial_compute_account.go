package state

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (t *pgFinancialComputeTxn) accountBook(month time.Time) (financial.ComputeBook, error) {
	raw, err := t.q.FinancialComputeAccountBookGet(t.ctx, t.db, sqlc.FinancialComputeAccountBookGetParams{AccountID: t.account, PeriodStart: pgTime(month)})
	var b financial.ComputeBook
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &b)
	}
	return b, err
}

func (t *pgFinancialComputeTxn) putAccountBook(b financial.ComputeBook) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return t.q.FinancialComputeAccountBookPut(t.ctx, t.db, sqlc.FinancialComputeAccountBookPutParams{AccountID: t.account, PeriodStart: pgTime(b.PeriodStart), PeriodEnd: pgTime(b.PeriodEnd), Payload: raw})
}

func (t *pgFinancialComputeTxn) allowance(month time.Time) (int64, error) {
	rows, err := t.q.FinancialPriceSnapshotsList(t.ctx, t.db, sqlc.FinancialPriceSnapshotsListParams{AccountID: t.account, PeriodStart: pgTime(month)})
	if err != nil {
		return 0, err
	}
	prices := make([]FinancialPriceSnapshot, 0, len(rows))
	for _, row := range rows {
		var p financial.Price
		if err := json.Unmarshal(row.Price, &p); err != nil {
			return 0, err
		}
		prices = append(prices, FinancialPriceSnapshot{Price: p, PeriodStart: row.PeriodStart.Time, EffectiveFrom: row.EffectiveFrom.Time})
	}
	return computeAllowance(prices, month, t.now)
}

func (t *pgFinancialComputeTxn) ensureAccount(policies []FinancialBudget) error {
	for _, p := range policies {
		if p.Spec.Enabled && financial.StrictAccountCompute(p.Spec) {
			b, err := t.book("", financial.ComputeMonth(t.now))
			if errors.Is(err, ErrNotFound) {
				if err = t.initializeAccount(); err != nil {
					return err
				}
				b, err = t.book("", financial.ComputeMonth(t.now))
			}
			if err != nil {
				return err
			}
			if financial.StrictAccountEgress(p.Spec) && !b.Egress.Initialized {
				return t.initializeEgress()
			}
			// Another account policy may add the egress meter.
			continue
		}
	}
	return nil
}

func (t *pgFinancialComputeTxn) accountReady() (bool, error) {
	owners, err := t.q.FinancialComputeOwnersReady(t.ctx, t.db, 3*api.FinancialComputeOwnerHeartbeat.Seconds())
	if err != nil || !owners.Valid || !owners.Bool {
		return false, err
	}
	safe, err := t.q.FinancialComputeAccountActivationSafe(t.ctx, t.db, t.account)
	if err != nil || !safe.Valid || !safe.Bool {
		return false, err
	}
	ready, err := t.q.FinancialComputeAccountSamplingReady(t.ctx, t.db, sqlc.FinancialComputeAccountSamplingReadyParams{AccountID: t.account, PeriodStart: pgTime(financial.ComputeMonth(t.now))})
	return ready.Valid && ready.Bool, err
}

func (t *pgFinancialComputeTxn) initializeAccount() error {
	ready, err := t.accountReady()
	if err != nil {
		return err
	}
	if !ready {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.accountBook(month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: pgUUIDString(t.account), PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	grant, err := t.allowance(month)
	if err != nil {
		return err
	}
	baseline, err := t.q.FinancialComputeAccountBaseline(t.ctx, t.db, sqlc.FinancialComputeAccountBaselineParams{AccountID: t.account, PeriodStart: pgTime(month), PeriodEnd: pgTime(b.PeriodEnd), UpperRate: api.OverageMillicentsPerGBHour, UpperQuantity: api.SecondsPerGBHour})
	if err != nil {
		return err
	}
	if baseline.Incomplete {
		return financial.ErrComputeUnavailable
	}
	quantity, err := strconv.ParseInt(baseline.Quantity, 10, 64)
	if err != nil || quantity < 0 {
		return financial.ErrOverflow
	}
	b.QuantityCommitted = max(b.QuantityCommitted, quantity)
	b.AllowanceGranted = max(b.AllowanceGranted, grant)
	if err := computeAccountReprice(&b); err != nil {
		return err
	}
	return t.putAccountBook(b)
}
