// Package consumerbilling lets an app's customers read their own usage,
// spend and invoices with their own consumer key (ADR-1506). Like the other
// apid domains (ADR-982), it reaches apid only through Host; apidserver keeps
// the route table.
package consumerbilling

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what consumer billing needs from apid.
type Host interface {
	Store() state.Store
	// QuoteConsumerUsage prices usage in [since, until) like a statement.
	QuoteConsumerUsage(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error)
	// Released reports whether API monetization is released to an account
	// (ADR-20261020170649).
	Released(accountID string) bool
	// OfferedPlans lists the plans sign-up offers with their current prices
	// (ADR-20261021143000).
	OfferedPlans(ctx context.Context, app state.App, settings state.APIConsumerSignupSettings) ([]api.ConsumerSignupPlan, error)
	// StripeKey opens the account's Stripe billing connection (ADR-1502).
	StripeKey(ctx context.Context, accountID string) (key string, livemode bool, problem *api.Problem)
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
}

// Service serves the consumer billing endpoints. Now is the clock; nil uses
// time.Now.
type Service struct {
	Host Host
	Now  func() time.Time
	// Stripe is the customer portal client (ADR-20261021183000); nil uses
	// the real API.
	Stripe *stripeinvoice.Client

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// limiterCap bounds the per-prefix limiter map; it is reset when full.
const limiterCap = 10000

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// allow applies the per-key-prefix rate limit before any database lookup.
func (s *Service) allow(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limiters == nil || len(s.limiters) >= limiterCap {
		s.limiters = map[string]*rate.Limiter{}
	}
	limiter, ok := s.limiters[prefix]
	if !ok {
		limiter = rate.NewLimiter(rate.Limit(float64(api.ConsumerBillingRequestsPerMinute)/60), api.ConsumerBillingBurst)
		s.limiters[prefix] = limiter
	}
	return limiter.Allow()
}

// caller is the app and consumer a request's key resolved to.
type caller struct {
	app      state.App
	consumer state.APIConsumer
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="consumer-billing"`)
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Unauthorized",
		"send a valid consumer key with the billing scope as a Bearer token"))
}

// authenticate resolves the request's consumer key. Every failure is the
// same 401, except a valid key without the billing scope (403).
func (s *Service) authenticate(w http.ResponseWriter, r *http.Request) (caller, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !api.ValidConsumerKeyFormat(token) {
		unauthorized(w)
		return caller{}, false
	}
	prefix, _, _ := strings.Cut(strings.TrimPrefix(token, api.ConsumerKeyPrefix), "_")
	if !s.allow(prefix) {
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeConsumerBillingRateLimited, "Too many requests",
			"consumer billing allows 60 requests per minute per key").WithLimit(api.ConsumerBillingRequestsPerMinute, api.ConsumerBillingRequestsPerMinute+1))
		return caller{}, false
	}
	ctx := r.Context()
	app, err := s.Host.Store().AppBySlug(ctx, r.PathValue("slug"))
	if err != nil {
		unauthorized(w)
		return caller{}, false
	}
	key, err := s.Host.Store().ConsumerKeyByAppAndPrefix(ctx, app.AccountID, app.ID, prefix)
	if err != nil || !api.ConstantTimeEqualHash(api.HashConsumerKey(token), key.Hash) || key.RevokedAt != nil ||
		(key.ExpiresAt != nil && !key.ExpiresAt.After(s.now())) || key.ConsumerID == "" {
		unauthorized(w)
		return caller{}, false
	}
	if !slices.Contains(key.Scopes, api.ConsumerScopeBilling) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Billing scope required",
			"ask the app owner for a consumer key with the billing scope"))
		return caller{}, false
	}
	consumer, err := s.Host.Store().GetAPIConsumerByID(ctx, app.AccountID, key.ConsumerID)
	if err != nil || consumer.AppID != app.ID || !consumer.Active() {
		unauthorized(w)
		return caller{}, false
	}
	if !s.Host.Released(app.AccountID) {
		// The app owner does not have API monetization: no such route.
		httpkit.NoSuchRoute(w)
		return caller{}, false
	}
	return caller{app: app, consumer: consumer}, true
}

// Summary returns the consumer's month to date.
func (s *Service) Summary(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	now := s.now()
	monthStart := billing.MonthStart(now)
	quote, err := s.Host.QuoteConsumerUsage(r.Context(), c.app.AccountID, c.app.ID, c.consumer.ID, monthStart, now.Truncate(time.Minute).Add(time.Minute))
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not price usage"))
		return
	}
	out := api.ConsumerBillingSummaryResponse{ConsumerID: c.consumer.ID, ExternalRef: c.consumer.ExternalRef, Name: c.consumer.Name,
		App: c.app.Slug, Month: monthStart.Format("2006-01"), Currency: quote.Currency, BillableUnits: quote.BillableUnits,
		UnpricedUnits: quote.UnpricedUnits, AmountMillicents: quote.AmountMillicents, Priced: quote.Priced, AsOf: now}
	out.Plan = s.currentPlan(r.Context(), c, now)
	out.Credits, out.SpendCap = s.prepaid(r.Context(), c, now)
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// prepaid reads the consumer's usable credit per currency and its spend cap
// (ADR-1306); a store without them reports none.
func (s *Service) prepaid(ctx context.Context, c caller, now time.Time) ([]api.APIConsumerCreditBalance, *api.ConsumerBillingSpendCap) {
	balances := []api.APIConsumerCreditBalance{}
	if credits, ok := s.Host.Store().(state.APIConsumerCreditStore); ok {
		grants, _ := credits.ListAPIConsumerCreditGrants(ctx, c.app.AccountID, c.app.ID, c.consumer.ID)
		sums := map[string]int64{}
		for _, g := range grants {
			if g.Usable(now) {
				sums[g.Currency] += g.RemainingMillicents
			}
		}
		for currency, available := range sums {
			balances = append(balances, api.APIConsumerCreditBalance{Currency: currency, AvailableMillicents: available})
		}
		sort.Slice(balances, func(i, j int) bool { return balances[i].Currency < balances[j].Currency })
	}
	caps, ok := s.Host.Store().(state.APIConsumerSpendCapStore)
	if !ok {
		return balances, nil
	}
	spendCap, err := caps.GetAPIConsumerSpendCap(ctx, c.app.AccountID, c.consumer.ID)
	if err != nil {
		return balances, nil
	}
	out := &api.ConsumerBillingSpendCap{Currency: spendCap.Currency, MonthlyCapMillicents: spendCap.MonthlyCapMillicents,
		Capped: spendCap.CappedIn(billing.MonthStart(now))}
	if spendCap.MonthStart != nil && spendCap.MonthStart.Equal(billing.MonthStart(now)) {
		out.AmountDueMillicents = spendCap.AmountDueMillicents
	}
	return balances, out
}

func (s *Service) currentPlan(ctx context.Context, c caller, now time.Time) *api.ConsumerBillingPlan {
	plans, ok := s.Host.Store().(state.APIConsumerPlanStore)
	if !ok {
		return nil
	}
	policy, err := plans.GetAPIConsumerPlanPolicy(ctx, c.app.AccountID, c.app.ID, c.consumer.ID, now)
	if err != nil || policy.PlanID == "" {
		return nil
	}
	plan, err := plans.GetAPIConsumerPlan(ctx, c.app.AccountID, c.app.ID, policy.PlanID)
	if err != nil {
		return nil
	}
	return &api.ConsumerBillingPlan{Name: plan.Name, MaxUnitsPerMonth: policy.MaxUnitsPerMonth}
}

// Usage returns the consumer's daily usage, at most ConsumerBillingUsageMaxDays.
func (s *Service) Usage(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	since, until, problem := s.usageWindow(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	usageStore, ok := s.Host.Store().(state.ConsumerUsageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("usage is unavailable"))
		return
	}
	buckets, err := usageStore.ListAPIConsumerUsage(r.Context(), c.app.AccountID, c.app.ID, c.consumer.ID, since, until)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrInternal("could not load usage"))
		return
	}
	days := map[string]*api.ConsumerBillingDay{}
	for _, b := range buckets {
		date := b.WindowStart.UTC().Format(time.DateOnly)
		day, ok := days[date]
		if !ok {
			day = &api.ConsumerBillingDay{Date: date}
			days[date] = day
		}
		day.Requests += b.RequestCount
		day.BillableUnits += b.BillableUnits
	}
	out := api.ConsumerBillingUsageResponse{Since: since, Until: until, Days: make([]api.ConsumerBillingDay, 0, len(days))}
	for _, day := range days {
		out.Days = append(out.Days, *day)
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Date < out.Days[j].Date })
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) usageWindow(r *http.Request) (time.Time, time.Time, *api.Problem) {
	now := s.now()
	since, until := billing.MonthStart(now), now.Truncate(time.Minute).Add(time.Minute)
	invalid := func(detail string) (time.Time, time.Time, *api.Problem) {
		return time.Time{}, time.Time{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid usage window", detail)
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return invalid("since must be RFC3339")
		}
		since = t.UTC().Truncate(time.Minute)
	}
	if raw := r.URL.Query().Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return invalid("until must be RFC3339")
		}
		until = t.UTC().Truncate(time.Minute)
	}
	if !until.After(since) {
		return invalid("until must be after since")
	}
	if until.Sub(since) > time.Duration(api.ConsumerBillingUsageMaxDays)*24*time.Hour {
		return time.Time{}, time.Time{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid usage window",
			"a usage window covers at most 31 days").WithLimit(api.ConsumerBillingUsageMaxDays, int64(until.Sub(since)/(24*time.Hour)))
	}
	return since, until, nil
}

// Statements returns the consumer's latest finalized statements with their
// exported invoice's customer-facing status.
func (s *Service) Statements(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	statements, ok := s.Host.Store().(state.APIConsumerUsageStatementStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("statements are unavailable"))
		return
	}
	all, err := statements.ListAPIConsumerUsageStatements(r.Context(), c.app.AccountID, c.app.ID, c.consumer.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list statements"))
		return
	}
	finalized := slices.DeleteFunc(all, func(st state.APIConsumerUsageStatement) bool {
		return st.Status != state.APIConsumerUsageStatementFinalized
	})
	sort.Slice(finalized, func(i, j int) bool {
		if !finalized[i].PeriodStart.Equal(finalized[j].PeriodStart) {
			return finalized[i].PeriodStart.After(finalized[j].PeriodStart)
		}
		return finalized[i].Revision > finalized[j].Revision
	})
	if len(finalized) > api.ConsumerBillingStatementsMax {
		finalized = finalized[:api.ConsumerBillingStatementsMax]
	}
	exports, _ := s.Host.Store().(state.StatementInvoiceExportStore)
	out := api.ConsumerBillingStatementsResponse{Statements: make([]api.ConsumerBillingStatement, 0, len(finalized))}
	for _, st := range finalized {
		item := api.ConsumerBillingStatement{ID: st.ID, PeriodStart: st.PeriodStart.UTC(), PeriodEnd: st.PeriodEnd.UTC(),
			Revision: st.Revision, Currency: st.Currency, BillableUnits: st.BillableUnits, AmountMillicents: st.AmountMillicents,
			CreditAppliedMillicents: st.CreditAppliedMillicents, AmountDueMillicents: st.AmountDueMillicents(), FinalizedAt: st.FinalizedAt}
		if exports != nil {
			if e, err := exports.GetStatementInvoiceExport(r.Context(), c.app.AccountID, state.StatementSourceAppConsumer, st.ID); err == nil &&
				e.Status == state.StatementInvoiceExportCompleted {
				item.Invoice = &api.ConsumerBillingInvoice{PaymentStatus: e.PaymentStatus, AmountMinor: e.AmountMinor,
					AmountRemainingMinor: e.AmountRemainingMinor, DueAt: e.DueAt, PaidAt: e.PaidAt, HostedInvoiceURL: e.HostedInvoiceURL}
			}
		}
		out.Statements = append(out.Statements, item)
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
