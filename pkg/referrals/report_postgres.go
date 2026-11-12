package referrals

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

// ManagerReport reads comparisons and collections from one database snapshot.
func (s *PostgresStore) ManagerReport(ctx context.Context, scope Scope, f ReportFilter) (ManagerReport, error) {
	if !scope.All {
		return ManagerReport{}, ErrReportAccess
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ManagerReport{}, fmt.Errorf("begin sales report: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := s.readManagerReport(ctx, tx, f)
	if err != nil {
		return ManagerReport{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ManagerReport{}, fmt.Errorf("finish sales report: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) readManagerReport(ctx context.Context, tx pgx.Tx, f ReportFilter) (ManagerReport, error) {
	out := ManagerReport{Reps: []ReportRep{}, Rows: []RepComparison{}, Collections: []FirstInvoiceCollections{}}
	observed, err := s.q.SalesReportClock(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("read sales report clock: %w", err)
	}
	out.ObservedAt = observed.Time.UTC()
	if f.Timezone == "" {
		f.Timezone = "UTC"
	}
	rows, err := s.q.SalesReportComparison(ctx, tx, sqlc.SalesReportComparisonParams{MemberEmails: f.MemberEmails, FromDate: dateValue(f.From), ToDate: dateValue(f.To), ReportTimezone: f.Timezone, OwnerFilter: f.Owner})
	if err != nil {
		return out, fmt.Errorf("read sales comparisons: %w", err)
	}
	for _, r := range rows {
		rep := ReportRep{ID: idString(r.ID), Email: r.Email, CurrentMember: r.CurrentMember}
		out.Reps = append(out.Reps, rep)
		if f.Owner != "" && f.Owner != rep.ID {
			continue
		}
		row := RepComparison{ReportRep: rep, Leads: LeadMetrics{Total: r.Total, New: r.New, Contacted: r.Contacted, Qualified: r.Qualified, Demo: r.Demo, Won: r.Won, Lost: r.Lost}, Signups: r.Signups, FirstPayments: r.FirstPayments, MissingInvoices: r.MissingInvoices}
		if r.AvgFirstPaymentSeconds.Valid {
			value := r.AvgFirstPaymentSeconds.Int64
			row.AvgFirstPaymentSeconds = &value
		}
		out.Rows = append(out.Rows, row)
	}
	collections, err := s.q.SalesReportCollections(ctx, tx, sqlc.SalesReportCollectionsParams{FromDate: dateValue(f.From), ToDate: dateValue(f.To), ReportTimezone: f.Timezone, OwnerFilter: f.Owner})
	if err != nil {
		return out, fmt.Errorf("read sales collections: %w", err)
	}
	for _, r := range collections {
		out.Collections = append(out.Collections, FirstInvoiceCollections{OwnerID: idString(r.OwnerID), Currency: r.Currency, Invoices: r.Invoices, PaidCents: r.PaidCents, RefundedCents: r.RefundedCents, PendingRefundCents: r.PendingRefundCents, RetainedCents: r.RetainedCents})
	}
	return out, nil
}
