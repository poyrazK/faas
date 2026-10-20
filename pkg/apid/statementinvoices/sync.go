package statementinvoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Start follows exported invoices' payment status and sends the daily
// overdue digest (ADR-1504) every StatementInvoiceSyncTickInterval.
func (s *Service) Start(ctx context.Context, log *slog.Logger) {
	if _, ok := s.Host.Store().(state.StatementInvoicePaymentStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.StatementInvoiceSyncTickInterval)
		defer ticker.Stop()
		for {
			if err := s.tick(ctx); err != nil && ctx.Err() == nil {
				log.Warn("statement invoice sync failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// SyncOnce is one worker tick: sync a batch of due invoices, then send the
// overdue digests.
func (s *Service) SyncOnce(ctx context.Context) error {
	payments, okPayments := s.Host.Store().(state.StatementInvoicePaymentStore)
	exports, okExports := s.Host.Store().(state.StatementInvoiceExportStore)
	if !okPayments || !okExports {
		return nil
	}
	now := s.now()
	due, err := payments.ClaimStatementInvoiceSyncs(ctx, now, api.StatementInvoiceSyncLease, api.StatementInvoiceSyncBatchSize)
	if err != nil {
		return fmt.Errorf("claim statement invoice syncs: %w", err)
	}
	conns := map[string]*connection{}
	for _, export := range due {
		if err := s.syncInvoice(ctx, payments, exports, conns, export, now); err != nil {
			return err
		}
	}
	return s.notifyOverdue(ctx, payments, now)
}

// syncInvoice reads one invoice from its provider and records its status.
// Errors reach the caller only when the store fails; provider failures
// defer. conns caches each account's opened connection per provider; nil
// marks one that is missing or rejected.
func (s *Service) syncInvoice(ctx context.Context, payments state.StatementInvoicePaymentStore, exports state.StatementInvoiceExportStore,
	conns map[string]*connection, export state.StatementInvoiceExport, now time.Time) error {
	p := s.provider(export.Provider)
	cacheKey := export.AccountID + "/" + export.Provider
	conn, ok := conns[cacheKey]
	if !ok && p != nil {
		if stored, err := exports.GetBillingConnection(ctx, export.AccountID, export.Provider); err == nil {
			if key, opened := s.openKey(export.AccountID, stored); opened {
				conn = &connection{key: key, apiURL: stored.APIURL, livemode: stored.Livemode}
			}
		}
		conns[cacheKey] = conn
		if conn == nil {
			s.notifySyncBlocked(ctx, export.AccountID, export.Provider, now)
		}
	}
	if conn == nil {
		return payments.DeferStatementInvoiceSync(ctx, export.ID, now.Add(api.StatementInvoiceSyncKeyRetry))
	}
	payment, known, err := p.payment(ctx, *conn, export.ProviderInvoiceID)
	if errors.Is(err, errKeyRejected) {
		conns[cacheKey] = nil
		s.notifySyncBlocked(ctx, export.AccountID, export.Provider, now)
		return payments.DeferStatementInvoiceSync(ctx, export.ID, now.Add(api.StatementInvoiceSyncKeyRetry))
	}
	if err != nil || !known {
		return payments.DeferStatementInvoiceSync(ctx, export.ID, now.Add(api.StatementInvoiceSyncRetry))
	}
	prev, err := payments.RecordStatementInvoicePayment(ctx, export.ID, payment, now, nextInvoiceSync(payment, now))
	if err != nil {
		return fmt.Errorf("record statement invoice payment: %w", err)
	}
	if prev != payment.Status {
		s.Host.Audit(ctx, "statement_invoice.status_changed", export.AccountID, map[string]any{"export_id": export.ID,
			"statement_id": export.StatementID, "source_kind": export.SourceKind, "invoice_id": export.ProviderInvoiceID,
			"from": prev, "to": payment.Status})
	}
	return nil
}

// notifySyncBlocked emails the account, at most once a day per provider,
// that Gregale cannot read its exported invoices: payment status stops
// updating, and overdue holds lift after api.ConsumerOverdueStatusMaxAge
// (ADR-20261019180000). A failed send is retried at the next sync.
func (s *Service) notifySyncBlocked(ctx context.Context, accountID, provider string, now time.Time) {
	acct, err := s.Host.Store().AccountByID(ctx, accountID)
	if err != nil {
		return
	}
	title := provider
	if p := s.provider(provider); p != nil {
		title = p.title()
	}
	day := now.UTC().Format(time.DateOnly)
	subject := "Gregale cannot read your " + title + " invoices"
	body := fmt.Sprintf("Gregale could not open or use your %s key, so the payment status of invoices it exported to %s "+
		"stopped updating. Customers held for an overdue invoice there are let through after %d hours without a fresh status.\n\n"+
		"Reconnect %s with: gregale account billing-connection connect %s\n(as of %s)\n",
		title, title, int(api.ConsumerOverdueStatusMaxAge.Hours()), title, provider, now.UTC().Format(time.RFC3339))
	_ = s.Host.SendEmail(ctx, acct.Email, subject, body, "statement-invoices-sync-blocked-"+accountID+"-"+provider+"-"+day)
}

// nextInvoiceSync is when to read the invoice again; nil once it is final.
func nextInvoiceSync(p state.StatementInvoicePayment, now time.Time) *time.Time {
	var interval time.Duration
	switch p.Status {
	case state.InvoicePaymentDraft:
		interval = api.StatementInvoiceDraftSyncInterval
	case state.InvoicePaymentOpen:
		interval = api.StatementInvoiceOpenSyncInterval
		if p.DueAt != nil && p.DueAt.Before(now) {
			interval = api.StatementInvoiceOverdueSyncInterval
		}
	case state.InvoicePaymentUncollectible:
		interval = api.StatementInvoiceUncollectibleSyncInterval
	default:
		return nil
	}
	next := now.Add(interval)
	return &next
}

// notifyOverdue emails each account one digest a day of invoices that were
// already overdue when the UTC day began, reporting each invoice once.
func (s *Service) notifyOverdue(ctx context.Context, payments state.StatementInvoicePaymentStore, now time.Time) error {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	overdue, err := payments.ListOverdueStatementInvoices(ctx, dayStart, 500)
	if err != nil {
		return fmt.Errorf("list overdue statement invoices: %w", err)
	}
	byAccount := map[string][]state.StatementInvoiceExport{}
	for _, export := range overdue {
		byAccount[export.AccountID] = append(byAccount[export.AccountID], export)
	}
	accounts := make([]string, 0, len(byAccount))
	for accountID := range byAccount {
		accounts = append(accounts, accountID)
	}
	sort.Strings(accounts)
	for _, accountID := range accounts {
		acct, err := s.Host.Store().AccountByID(ctx, accountID)
		if err != nil {
			continue
		}
		invoices := byAccount[accountID]
		subject, body := overdueDigest(invoices, now)
		if err := s.Host.SendEmail(ctx, acct.Email, subject, body, "statement-invoices-overdue-"+accountID+"-"+dayStart.Format(time.DateOnly)); err != nil {
			continue // the next tick retries
		}
		ids := make([]string, len(invoices))
		for i, invoice := range invoices {
			ids[i] = invoice.ID
		}
		if err := payments.MarkStatementInvoicesOverdueNotified(ctx, ids, now); err != nil {
			return fmt.Errorf("mark overdue invoices notified: %w", err)
		}
	}
	return nil
}

func overdueDigest(invoices []state.StatementInvoiceExport, now time.Time) (string, string) {
	subject := fmt.Sprintf("%d invoices from Gregale usage statements are overdue", len(invoices))
	if len(invoices) == 1 {
		subject = "An invoice from a Gregale usage statement is overdue"
	}
	var b strings.Builder
	b.WriteString("These invoices Gregale exported to your billing provider from usage statements are past due:\n\n")
	for i, invoice := range invoices {
		if i == api.StatementInvoiceOverdueDigestMax {
			fmt.Fprintf(&b, "  …and %d more\n", len(invoices)-i)
			break
		}
		remaining := invoice.AmountMinor
		if invoice.AmountRemainingMinor != nil {
			remaining = *invoice.AmountRemainingMinor
		}
		link := dashboardURL(invoice.Provider, invoice.Livemode, invoice.ProviderInvoiceID)
		if link == "" {
			link = invoice.HostedInvoiceURL
		}
		fmt.Fprintf(&b, "  %s %s  %s  %s remaining  due %s  %s\n", invoice.Provider, invoice.ProviderInvoiceID, invoice.Customer,
			formatMinor(invoice.Currency, remaining), invoice.DueAt.UTC().Format(time.DateOnly), link)
	}
	fmt.Fprintf(&b, "\nList unpaid invoices with: gregale account invoices --status overdue\n(as of %s)\n", now.Format(time.RFC3339))
	return subject, b.String()
}

// formatMinor renders an amount in currency's minor unit, e.g. EUR 37.50,
// JPY 3750 or KWD 3.750 (ADR-20261019100330).
func formatMinor(currency string, minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	exp, _ := api.CurrencyMinorExponent(currency)
	if exp == 0 {
		return fmt.Sprintf("%s %s%d", currency, sign, minor)
	}
	scale := int64(1)
	for range exp {
		scale *= 10
	}
	return fmt.Sprintf("%s %s%d.%0*d", currency, sign, minor/scale, exp, minor%scale)
}
