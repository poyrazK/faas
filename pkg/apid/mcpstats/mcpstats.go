// Package mcpstats serves GET /v1/apps/{slug}/mcp-stats (ADR-20261126082046):
// a sold MCP server's customer funnel, revenue and units per tool, and the
// customers near their plan's limit, over UTC calendar months.
package mcpstats

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is what the stats read.
type Store interface {
	ListAPIConsumersForApp(ctx context.Context, accountID, appID string) ([]state.APIConsumer, error)
	ListAPIConsumerPlans(ctx context.Context, accountID, appID string) ([]state.APIConsumerPlan, error)
	state.SellerStatsStore
}

// SignupLister tells which customers came through the hosted sign-up page
// with a checkout (their external ref is the provider's customer id); a store
// without it counts them as the owner's.
type SignupLister interface {
	ListAPIConsumerSignups(ctx context.Context, accountID, appID string, limit int) ([]state.APIConsumerSignup, error)
}

// Quoter prices a consumer's usage in [since, until) exactly as statements
// do, with the route breakdown.
type Quoter func(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error)

// App identifies the app whose ownership the caller resolved.
type App struct {
	Slug, ID, AccountID string
}

// ErrMonths refuses a window outside 1..api.MCPSellerStatsMaxMonths.
var ErrMonths = fmt.Errorf("months must be between 1 and %d", api.MCPSellerStatsMaxMonths)

const toolPrefix = api.MCPBillingToolsPrefix + "/"

// Read builds the stats for the last months calendar months ending now.
func Read(ctx context.Context, store Store, quote Quoter, app App, months int, now time.Time) (api.MCPSellerStatsResponse, error) {
	if months < 1 || months > api.MCPSellerStatsMaxMonths {
		return api.MCPSellerStatsResponse{}, ErrMonths
	}
	now = now.UTC()
	r := &reader{store: store, quote: quote, app: app, now: now, thisMonth: state.UTCMonthStart(now)}
	r.start = r.thisMonth.AddDate(0, -(months - 1), 0)
	r.until = now.Truncate(time.Minute).Add(time.Minute)
	r.resp = api.MCPSellerStatsResponse{App: app.Slug, GeneratedAt: now, Tools: []api.MCPSellerStatsTool{},
		NearLimit: []api.MCPSellerNearLimit{}, EstimateComplete: true}
	r.months = map[string]*api.MCPSellerStatsMonth{}
	for m := r.start; !m.After(r.thisMonth); m = m.AddDate(0, 1, 0) {
		key := m.Format("2006-01")
		r.resp.Months = append(r.resp.Months, api.MCPSellerStatsMonth{Month: key, Current: m.Equal(r.thisMonth),
			SignedUpBySource: map[string]int64{}, Revenue: []api.MCPSellerStatsAmount{}})
	}
	for i := range r.resp.Months {
		r.months[r.resp.Months[i].Month] = &r.resp.Months[i]
	}
	steps := []struct {
		name string
		run  func(context.Context) error
	}{{"customers", r.customers}, {"usage by month", r.usage}, {"statements", r.billed}, {"estimate", r.estimate},
		{"tool usage", r.tools}, {"near limit", r.nearLimit}}
	for _, step := range steps {
		if err := step.run(ctx); err != nil {
			return api.MCPSellerStatsResponse{}, fmt.Errorf("seller stats: %s: %w", step.name, err)
		}
	}
	return r.resp, nil
}

type reader struct {
	store                        Store
	quote                        Quoter
	app                          App
	now, thisMonth, start, until time.Time
	resp                         api.MCPSellerStatsResponse
	months                       map[string]*api.MCPSellerStatsMonth
	consumers                    []state.APIConsumer
	// unitsNow is each consumer's billable units this month.
	unitsNow map[string]int64
	// toolRevenue is this month's estimate per tool and currency.
	toolRevenue map[string]map[string]int64
}

func (r *reader) month(t time.Time) *api.MCPSellerStatsMonth {
	return r.months[t.UTC().Format("2006-01")]
}

// customers counts sign-ups by source, closures and today's totals.
func (r *reader) customers(ctx context.Context) error {
	consumers, err := r.store.ListAPIConsumersForApp(ctx, r.app.AccountID, r.app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	r.consumers = consumers
	signups, err := r.signups(ctx)
	if err != nil {
		return err
	}
	for _, c := range consumers {
		if c.RevokedAt != nil {
			r.resp.Customers.Closed++
			if m := r.month(*c.RevokedAt); m != nil {
				m.Closed++
			}
		} else {
			r.resp.Customers.Open++
		}
		if m := r.month(c.CreatedAt); m != nil {
			m.SignedUp++
			m.SignedUpBySource[source(c, signups)]++
		}
	}
	return nil
}

// signups is the set of consumers a hosted sign-up created.
func (r *reader) signups(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	l, ok := r.store.(SignupLister)
	if !ok {
		return out, nil
	}
	signups, err := l.ListAPIConsumerSignups(ctx, r.app.AccountID, r.app.ID, api.MCPSellerStatsMaxSignups)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("list sign-ups: %w", err)
	}
	for _, s := range signups {
		if s.ConsumerID != "" {
			out[s.ConsumerID] = true
		}
	}
	return out, nil
}

// source is how the customer was created, which never changes: an MCP
// sign-in creates a consumer whose external ref starts "oauth:", the hosted
// sign-up page one with a sign-up row (and, on the free plan, a "signup:"
// ref). An identity linked to an existing consumer later does not count.
func source(c state.APIConsumer, signups map[string]bool) string {
	switch {
	case strings.HasPrefix(c.ExternalRef, "oauth:"):
		return api.MCPSellerSourceOAuth
	case signups[c.ID], strings.HasPrefix(c.ExternalRef, "signup:"):
		return api.MCPSellerSourceSignup
	}
	return api.MCPSellerSourceOwner
}

// usage sums requests and units per month and marks active customers.
// Anonymous traffic counts toward requests and units but is no customer.
func (r *reader) usage(ctx context.Context) error {
	sums, err := r.store.SumAPIConsumerUsageByMonth(ctx, r.app.AccountID, r.app.ID, r.start, r.until)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	r.unitsNow = map[string]int64{}
	for _, s := range sums {
		m := r.month(s.Month)
		if m == nil {
			continue
		}
		m.Requests += s.Requests
		m.Units += s.BillableUnits
		if s.BillableUnits > 0 && s.ConsumerKey != state.AnonymousConsumerKey {
			m.Active++
		}
		if m.Current {
			r.unitsNow[s.ConsumerKey] += s.BillableUnits
		}
	}
	return nil
}

// billed sums the statements of closed months: a finalized statement is
// billed; a period the month-end run has not finalized yet counts its latest
// draft as an estimate.
func (r *reader) billed(ctx context.Context) error {
	if !r.thisMonth.After(r.start) {
		return nil
	}
	statements, err := r.store.ListAPIConsumerStatementsForApp(ctx, r.app.AccountID, r.app.ID, r.start, r.thisMonth)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	type period struct {
		consumer   string
		start, end time.Time
	}
	chosen := map[period]state.APIConsumerUsageStatement{}
	order := []period{}
	for _, s := range statements {
		p := period{s.ConsumerID, s.PeriodStart.UTC(), s.PeriodEnd.UTC()}
		prev, seen := chosen[p]
		if !seen {
			order = append(order, p)
		}
		final := s.Status == state.APIConsumerUsageStatementFinalized
		prevFinal := seen && prev.Status == state.APIConsumerUsageStatementFinalized
		if !seen || (final && !prevFinal) || (final == prevFinal && s.Revision > prev.Revision) {
			chosen[p] = s
		}
	}
	paying := map[string]map[string]bool{}
	for _, p := range order {
		s := chosen[p]
		m := r.month(s.PeriodStart)
		if m == nil || s.AmountMillicents <= 0 {
			continue
		}
		if s.Status == state.APIConsumerUsageStatementFinalized {
			addAmount(&m.Revenue, s.Currency, s.AmountMillicents, 0)
		} else {
			addAmount(&m.Revenue, s.Currency, 0, s.AmountMillicents)
		}
		if paying[m.Month] == nil {
			paying[m.Month] = map[string]bool{}
		}
		if !paying[m.Month][s.ConsumerID] {
			paying[m.Month][s.ConsumerID] = true
			m.Paying++
		}
	}
	return nil
}

// estimate prices this month so far for the open customers with the most
// units, at most api.MCPSellerStatsMaxQuotes of them,
// api.MCPSellerStatsQuoteParallelism at a time and within
// api.MCPSellerStatsQuoteBudget, so the read answers inside a client's
// request timeout. Customers left unpriced mark the estimate incomplete.
func (r *reader) estimate(ctx context.Context) error {
	current := r.month(r.thisMonth)
	candidates := []state.APIConsumer{}
	for _, c := range r.consumers {
		if c.RevokedAt == nil || !c.RevokedAt.Before(r.thisMonth) {
			candidates = append(candidates, c)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if r.unitsNow[candidates[i].ID] != r.unitsNow[candidates[j].ID] {
			return r.unitsNow[candidates[i].ID] > r.unitsNow[candidates[j].ID]
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) > api.MCPSellerStatsMaxQuotes {
		candidates, r.resp.EstimateComplete = candidates[:api.MCPSellerStatsMaxQuotes], false
	}
	r.toolRevenue = map[string]map[string]int64{}
	budget, cancel := context.WithTimeout(ctx, api.MCPSellerStatsQuoteBudget)
	defer cancel()
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		next = make(chan state.APIConsumer)
	)
	for range min(api.MCPSellerStatsQuoteParallelism, len(candidates)) {
		wg.Go(func() {
			for c := range next {
				q, err := r.quote(budget, r.app.AccountID, r.app.ID, c.ID, r.thisMonth, r.until)
				mu.Lock()
				if err != nil {
					// One customer's price history, or the time budget,
					// should not hide the rest.
					r.resp.EstimateComplete = false
				} else {
					r.addQuote(current, q)
				}
				mu.Unlock()
			}
		})
	}
send:
	for _, c := range candidates {
		select {
		case next <- c:
		case <-budget.Done():
			r.resp.EstimateComplete = false
			break send
		}
	}
	close(next)
	wg.Wait()
	return ctx.Err()
}

// addQuote adds one customer's priced month to the current month and tools.
func (r *reader) addQuote(current *api.MCPSellerStatsMonth, q billing.APIConsumerUsageQuote) {
	if q.AmountMillicents > 0 {
		current.Paying++
		addAmount(&current.Revenue, q.Currency, 0, q.AmountMillicents)
	}
	for _, b := range q.RouteBreakdown {
		tool, ok := strings.CutPrefix(b.Route, toolPrefix)
		if !ok || b.AmountMillicents <= 0 {
			continue
		}
		if r.toolRevenue[tool] == nil {
			r.toolRevenue[tool] = map[string]int64{}
		}
		r.toolRevenue[tool][q.Currency] += b.AmountMillicents
	}
}

// tools lists units and customers per MCP tool over the window.
func (r *reader) tools(ctx context.Context) error {
	totals, err := r.store.SumAPIConsumerRouteUsage(ctx, r.app.AccountID, r.app.ID, toolPrefix, r.start, r.until)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	for _, t := range totals {
		name := strings.TrimPrefix(t.Route, toolPrefix)
		tool := api.MCPSellerStatsTool{Tool: name, Units: t.BillableUnits, Customers: t.Consumers}
		for currency, amount := range r.toolRevenue[name] {
			addAmount(&tool.Revenue, currency, 0, amount)
		}
		r.resp.Tools = append(r.resp.Tools, tool)
	}
	return nil
}

// nearLimit lists open customers at api.MCPSellerStatsNearLimitPercent of
// their current plan's monthly units or more.
func (r *reader) nearLimit(ctx context.Context) error {
	plans, err := r.store.ListAPIConsumerPlans(ctx, r.app.AccountID, r.app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("list plans: %w", err)
	}
	byID := map[string]state.APIConsumerPlan{}
	for _, p := range plans {
		byID[p.ID] = p
	}
	assignments, err := r.store.ListAPIConsumerPlanAssignmentsForApp(ctx, r.app.AccountID, r.app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("list plan assignments: %w", err)
	}
	current := map[string]string{} // consumer → plan in force now
	for _, a := range assignments {
		if !a.EffectiveFrom.After(r.now) {
			current[a.ConsumerID] = a.PlanID
		}
	}
	for _, c := range r.consumers {
		plan, ok := byID[current[c.ID]]
		units := r.unitsNow[c.ID]
		if c.RevokedAt != nil || !ok || plan.MaxUnitsPerMonth <= 0 || units*100 < plan.MaxUnitsPerMonth*api.MCPSellerStatsNearLimitPercent {
			continue
		}
		r.resp.NearLimit = append(r.resp.NearLimit, api.MCPSellerNearLimit{ConsumerID: c.ID, Name: c.Name, Plan: plan.Name,
			Units: units, MaxUnitsPerMonth: plan.MaxUnitsPerMonth, Percent: units * 100 / plan.MaxUnitsPerMonth})
	}
	sort.SliceStable(r.resp.NearLimit, func(i, j int) bool {
		a, b := r.resp.NearLimit[i], r.resp.NearLimit[j]
		if a.Percent != b.Percent {
			return a.Percent > b.Percent
		}
		return a.Units > b.Units
	})
	if len(r.resp.NearLimit) > api.MCPSellerStatsNearLimitMax {
		r.resp.NearLimit = r.resp.NearLimit[:api.MCPSellerStatsNearLimitMax]
	}
	return nil
}

// addAmount adds billed and estimated millicents to the currency's entry,
// keeping the list sorted by currency.
func addAmount(list *[]api.MCPSellerStatsAmount, currency string, billed, estimated int64) {
	for i := range *list {
		if (*list)[i].Currency == currency {
			(*list)[i].BilledMillicents += billed
			(*list)[i].EstimatedMillicents += estimated
			return
		}
	}
	*list = append(*list, api.MCPSellerStatsAmount{Currency: currency, BilledMillicents: billed, EstimatedMillicents: estimated})
	sort.Slice(*list, func(i, j int) bool { return (*list)[i].Currency < (*list)[j].Currency })
}
