package state

import (
	"context"
	"sort"
	"time"
)

type APIConsumerMonetizationCohort struct {
	Total     int
	Customers []APIConsumerMonetizationCustomer
}

type APIConsumerMonetizationCustomer struct {
	ID           string
	Name         string
	RegisteredAt time.Time
	Periods      []APIConsumerMonetizationPeriod
}

type APIConsumerMonetizationPeriod struct {
	Start           time.Time
	End             time.Time
	StatementID     string
	Draft           bool
	UsageHoldRun    string
	InvoiceProvider string
	InvoiceLive     bool
	PaymentStatus   string
	SentAt          *time.Time
	PaidAt          *time.Time
}

type APIMonetizationFunnelStore interface {
	ListAPIConsumerMonetizationCohort(ctx context.Context, accountID, appID string, since, until time.Time, limit int) (APIConsumerMonetizationCohort, error)
}

func (m *MemStore) ListAPIConsumerMonetizationCohort(_ context.Context, accountID, appID string, since, until time.Time, limit int) (APIConsumerMonetizationCohort, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	testSignups := make(map[string]bool)
	for _, signup := range m.apiConsumerSignups {
		if signup.AccountID == accountID && signup.AppID == appID && signup.Status == APIConsumerSignupCompleted && !signup.Livemode {
			testSignups[signup.ConsumerID] = true
		}
	}
	var out APIConsumerMonetizationCohort
	for _, c := range m.apiConsumers {
		if c.AccountID == accountID && c.AppID == appID && !c.PlatformTenantManaged && !testSignups[c.ID] &&
			!c.CreatedAt.Before(since) && c.CreatedAt.Before(until) {
			out.Customers = append(out.Customers, APIConsumerMonetizationCustomer{ID: c.ID, Name: c.Name, RegisteredAt: c.CreatedAt})
		}
	}
	sort.Slice(out.Customers, func(i, j int) bool {
		a, b := out.Customers[i], out.Customers[j]
		if a.RegisteredAt.Equal(b.RegisteredAt) {
			return a.ID > b.ID
		}
		return a.RegisteredAt.After(b.RegisteredAt)
	})
	out.Total = len(out.Customers)
	out.Customers = out.Customers[:min(limit, out.Total)]
	if len(out.Customers) == 0 {
		return out, nil
	}
	byCustomer := make(map[string]*APIConsumerMonetizationCustomer, len(out.Customers))
	for i := range out.Customers {
		c := &out.Customers[i]
		byCustomer[c.ID] = c
	}
	exports := make(map[string]StatementInvoiceExport)
	for _, e := range m.statementInvoiceExports {
		if e.AccountID == accountID && e.SourceKind == StatementSourceAppConsumer && e.Status == StatementInvoiceExportCompleted && e.ProviderInvoiceID != "" {
			exports[e.StatementID] = e
		}
	}
	// Keep the most recent completed draft-run result for each statement.
	// A successful later run supersedes an earlier hold on that statement.
	type runResult struct {
		run  BillingRun
		item BillingRunItem
	}
	latestRuns := make(map[string]runResult)
	for _, run := range m.billingRuns {
		if run.AccountID != accountID || run.Status != BillingRunCompleted || run.Finalize ||
			run.Export != BillingRunExportNone || !run.PeriodEnd.After(since) || run.PeriodEnd.After(until) {
			continue
		}
		for _, item := range m.billingRunItems[run.ID] {
			if item.SourceKind != StatementSourceAppConsumer || item.AppID != appID || byCustomer[item.SourceID] == nil ||
				item.StatementID == "" {
				continue
			}
			statement, exists := m.apiConsumerUsageStatements[item.StatementID]
			if !exists || !run.PeriodStart.Equal(statement.PeriodStart) || !run.PeriodEnd.Equal(statement.PeriodEnd) {
				continue
			}
			prior := latestRuns[item.StatementID].run
			if run.CreatedAt.After(prior.CreatedAt) || run.CreatedAt.Equal(prior.CreatedAt) && run.ID > prior.ID {
				latestRuns[item.StatementID] = runResult{run: run, item: item}
			}
		}
	}
	for _, s := range m.apiConsumerUsageStatements {
		c := byCustomer[s.ConsumerID]
		if c == nil || s.AccountID != accountID || s.AppID != appID ||
			!s.PeriodEnd.After(c.RegisteredAt) || s.PeriodEnd.After(until) {
			continue
		}
		period := APIConsumerMonetizationPeriod{Start: s.PeriodStart, End: s.PeriodEnd, StatementID: s.ID}
		switch s.Status {
		case APIConsumerUsageStatementDraft:
			period.Draft = true
			result := latestRuns[s.ID]
			if result.item.Outcome == BillingRunItemHeld && result.item.Completeness == "gaps_detected" &&
				result.item.UsageRecoveredAt == nil {
				period.UsageHoldRun = result.run.ID
			}
			if period.UsageHoldRun == "" && s.AmountDueMillicents() <= 0 && s.UnpricedUnits == 0 {
				continue
			}
		case APIConsumerUsageStatementFinalized:
			if s.UnpricedUnits != 0 || s.AmountDueMillicents() <= 0 {
				continue
			}
			e := exports[s.ID]
			period.InvoiceProvider, period.InvoiceLive, period.PaymentStatus = e.Provider, e.Livemode, e.PaymentStatus
			if e.Provider == BillingProviderStripe && e.Livemode {
				if e.StripeIssue != nil {
					period.SentAt = e.StripeIssue.SentAt
				}
				record := APIConsumerBillingRecord{InvoiceID: e.ProviderInvoiceID, PaymentStatus: e.PaymentStatus,
					PaidAt: e.PaidAt, AmountPaidMinor: e.AmountPaidMinor}
				if record.ConfirmedPayment() {
					period.PaidAt = e.PaidAt
				}
			}
		default:
			continue
		}
		c.Periods = append(c.Periods, period)
	}
	return out, nil
}

func (s *PgStore) ListAPIConsumerMonetizationCohort(ctx context.Context, accountID, appID string, since, until time.Time, limit int) (APIConsumerMonetizationCohort, error) {
	// Count the entire registration cohort, but load statement history only for
	// the customers whose first month of usage will be priced by the handler.
	rows, err := s.pool.Query(ctx, `
		with cohort as (
			select c.id, c.name, c.created_at, count(*) over () as total
			from api_consumers c
			where c.account_id = $1::uuid and c.app_id = $2::uuid
			  and c.created_at >= $3 and c.created_at < $4
			  and not c.platform_tenant_managed
			  and not exists (
				select 1 from api_consumer_signups g
				 where g.account_id = c.account_id and g.app_id = c.app_id and g.consumer_id = c.id
				   and g.status = 'completed' and not g.livemode
			  )
			order by c.created_at desc, c.id desc
			limit $5
		)
		select c.id, c.name, c.created_at, c.total, s.period_start, s.period_end,
		       coalesce(s.id::text, ''), coalesce(s.status = 'draft', false),
		       coalesce(s.amount_millicents > s.credit_applied_millicents or s.unpriced_units > 0, false),
		       coalesce(hold.id, ''),
		       coalesce(e.provider, ''), coalesce(e.livemode, false), coalesce(e.payment_status, ''),
		       case when e.provider = 'stripe' and e.livemode then (e.stripe_issue->>'sent_at')::timestamptz end,
		       case when e.provider = 'stripe' and e.livemode and e.payment_status = 'paid' and e.amount_paid_minor > 0 then e.paid_at end
		  from cohort c
		  left join api_consumer_usage_statements s
		    on s.account_id = $1::uuid and s.app_id = $2::uuid and s.consumer_id = c.id
		   and (s.status = 'draft' or (s.status = 'finalized' and s.unpriced_units = 0
		        and s.amount_millicents > s.credit_applied_millicents))
		   and s.period_end > c.created_at and s.period_end <= $4
		  left join usage_statement_invoice_exports e
		    on e.account_id = s.account_id and e.statement_id = s.id and s.status = 'finalized'
		   and e.source_kind = 'app_consumer' and e.status = 'completed' and e.provider_invoice_id <> ''
		  left join lateral (
		    select case when i.outcome = 'held' and i.completeness = 'gaps_detected'
		                  and i.usage_recovered_at is null then r.id::text end as id
		      from billing_runs r
		      join billing_run_items i on i.run_id = r.id and i.source_kind = 'app_consumer'
		       and i.source_id = s.consumer_id and i.app_id = s.app_id and i.statement_id = s.id
		     where s.status = 'draft' and r.account_id = s.account_id and r.period_start = s.period_start
		       and r.period_end = s.period_end and r.status = 'completed' and not r.finalize and r.export_provider = 'none'
		     order by r.created_at desc, r.id desc
		     limit 1
		  ) hold on true
		 order by c.created_at desc, c.id desc`, accountID, appID, since, until, limit)
	if err != nil {
		return APIConsumerMonetizationCohort{}, err
	}
	defer rows.Close()

	var out APIConsumerMonetizationCohort
	for rows.Next() {
		var c APIConsumerMonetizationCustomer
		var start, end *time.Time
		var p APIConsumerMonetizationPeriod
		var actionableDraft bool
		if err := rows.Scan(&c.ID, &c.Name, &c.RegisteredAt, &out.Total, &start, &end, &p.StatementID, &p.Draft,
			&actionableDraft, &p.UsageHoldRun, &p.InvoiceProvider, &p.InvoiceLive, &p.PaymentStatus, &p.SentAt, &p.PaidAt); err != nil {
			return APIConsumerMonetizationCohort{}, err
		}
		last := len(out.Customers) - 1
		if last < 0 || out.Customers[last].ID != c.ID {
			out.Customers = append(out.Customers, c)
			last++
		}
		if start != nil && end != nil && (!p.Draft || actionableDraft || p.UsageHoldRun != "") {
			p.Start, p.End = *start, *end
			out.Customers[last].Periods = append(out.Customers[last].Periods, p)
		}
	}
	return out, rows.Err()
}
