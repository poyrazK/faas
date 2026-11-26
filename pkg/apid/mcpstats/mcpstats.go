// Package mcpstats serves GET /v1/apps/{slug}/mcp-stats (ADR-20261126082046):
// a sold MCP server's customer funnel, revenue and units per tool, and the
// customers near their plan's limit, over UTC calendar months.
package mcpstats

import (
	"context"
	"errors"
	"sort"
	"strings"
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

// IdentityLister and SignupLister tell where customers came from; a store
// without them counts every customer as the owner's.
type IdentityLister interface {
	ListAPIConsumerIdentities(ctx context.Context, accountID, appID string) ([]state.APIConsumerIdentity, error)
}

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

// signupListMax bounds the sign-ups read to tell sources apart.
const signupListMax = 100_000

// ErrMonths refuses a window outside 1..api.MCPSellerStatsMaxMonths.
var ErrMonths = errors.New("months must be between 1 and 12")

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
	for _, step := range []func(context.Context) error{r.customers, r.usage, r.billed, r.estimate, r.tools, r.nearLimit} {
		if err := step(ctx); err != nil {
			return api.MCPSellerStatsResponse{}, err
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
	sources, err := r.sources(ctx)
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
			m.SignedUpBySource[source(c, sources)]++
		}
	}
	return nil
}

func (r *reader) sources(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	if l, ok := r.store.(SignupLister); ok {
		signups, err := l.ListAPIConsumerSignups(ctx, r.app.AccountID, r.app.ID, signupListMax)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		for _, s := range signups {
			if s.ConsumerID != "" {
				out[s.ConsumerID] = api.MCPSellerSourceSignup
			}
		}
	}
	if l, ok := r.store.(IdentityLister); ok {
		identities, err := l.ListAPIConsumerIdentities(ctx, r.app.AccountID, r.app.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		for _, id := range identities {
			out[id.ConsumerID] = api.MCPSellerSourceOAuth
		}
	}
	return out, nil
}

func source(c state.APIConsumer, sources map[string]string) string {
	switch {
	case sources[c.ID] != "":
		return sources[c.ID]
	case strings.HasPrefix(c.ExternalRef, "oauth:"):
		return api.MCPSellerSourceOAuth
	case strings.HasPrefix(c.ExternalRef, "signup:"):
		return api.MCPSellerSourceSignup
	}
	return api.MCPSellerSourceOwner
}

// usage sums requests and units per month and marks active customers.
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
		if s.BillableUnits > 0 {
			m.Active++
		}
		if m.Current {
			r.unitsNow[s.ConsumerKey] += s.BillableUnits
		}
	}
	return nil
}

// billed sums finalized statements of closed months.
func (r *reader) billed(ctx context.Context) error {
	if !r.thisMonth.After(r.start) {
		return nil
	}
	statements, err := r.store.ListFinalizedAPIConsumerStatementsForApp(ctx, r.app.AccountID, r.app.ID, r.start, r.thisMonth)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	paying := map[string]map[string]bool{}
	for _, s := range statements {
		m := r.month(s.PeriodStart)
		if m == nil || s.AmountMillicents <= 0 {
			continue
		}
		addAmount(&m.Revenue, s.Currency, s.AmountMillicents, 0)
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
// units, at most api.MCPSellerStatsMaxQuotes of them.
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
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		q, err := r.quote(ctx, r.app.AccountID, r.app.ID, c.ID, r.thisMonth, r.until)
		if err != nil {
			// One customer's price history should not hide the rest.
			r.resp.EstimateComplete = false
			continue
		}
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
	return nil
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
		return err
	}
	byID := map[string]state.APIConsumerPlan{}
	for _, p := range plans {
		byID[p.ID] = p
	}
	assignments, err := r.store.ListAPIConsumerPlanAssignmentsForApp(ctx, r.app.AccountID, r.app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
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
