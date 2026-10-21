// Package consumersignup lets an app's API customers sign up themselves
// (ADR-20261021143000): they pick a plan, save a card with Stripe Checkout
// in the app owner's own Stripe account, and get a consumer key. Like the
// other apid domains (ADR-982), it reaches apid only through Host;
// apidserver keeps the route table.
package consumersignup

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what sign-up needs from apid.
type Host interface {
	Store() state.Store
	// Released reports whether API monetization is released to an account
	// (ADR-20261020170649).
	Released(accountID string) bool
	// AppRoute resolves the app of an owner route; on false it has written
	// the problem.
	AppRoute(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool)
	// ConsumerFeatureAllowed reports whether the plan includes API
	// consumers; on false it has written the problem.
	ConsumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool
	// KeyScopesProblem checks consumer key scopes for an account.
	KeyScopesProblem(accountID string, scopes []string) *api.Problem
	// StripeKey opens the account's Stripe billing connection (ADR-1502).
	StripeKey(ctx context.Context, accountID string) (key string, livemode bool, problem *api.Problem)
	// IssueConsumerKey creates a key for consumer within the account's key
	// quotas and returns its plaintext once.
	IssueConsumerKey(ctx context.Context, acct state.Account, consumer state.APIConsumer, name string, scopes []string) (state.ConsumerKey, string, *api.Problem)
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
	// PublicOrigin is the https origin the sign-up page is served from.
	PublicOrigin() string
	// ClientIP is the request's client address behind the trusted proxy.
	ClientIP(r *http.Request) string
}

// Service serves sign-up settings, the public sign-up API and page, and
// deletes stale sign-ups. Stripe nil uses the real API; Now nil uses
// time.Now.
type Service struct {
	Host   Host
	Stripe *stripeinvoice.Client
	Now    func() time.Time
	// Enabled pauses the cleanup worker while it reports false; nil runs it.
	Enabled func() bool

	mu      sync.Mutex
	creates map[string]*rate.Limiter
	reads   map[string]*rate.Limiter
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) stripe() *stripeinvoice.Client {
	if s.Stripe != nil {
		return s.Stripe
	}
	return stripeinvoice.New()
}

// limiterCap bounds each per-address limiter map; it is reset when full.
const limiterCap = 10000

// allow applies a per-address limit before any database read.
func (s *Service) allow(ip string, create bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	limiters, limit, burst := &s.reads, rate.Limit(float64(api.ConsumerSignupReadsPerIPPerMinute)/60), api.ConsumerSignupReadsPerIPPerMinute
	if create {
		limiters, limit, burst = &s.creates, rate.Limit(float64(api.ConsumerSignupPerIPPerHour)/3600), api.ConsumerSignupPerIPPerHour
	}
	if *limiters == nil || len(*limiters) >= limiterCap {
		*limiters = map[string]*rate.Limiter{}
	}
	limiter, ok := (*limiters)[ip]
	if !ok {
		limiter = rate.NewLimiter(limit, burst)
		(*limiters)[ip] = limiter
	}
	return limiter.Allow()
}

func (s *Service) signupStore(w http.ResponseWriter) (state.APIConsumerSignupStore, bool) {
	store, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer sign-up is unavailable"))
	}
	return store, ok
}

// PageURL is the hosted sign-up page of an app.
func (s *Service) PageURL(slug string) string {
	return s.Host.PublicOrigin() + "/v1/consumer-signup/" + url.PathEscape(slug) + "/page"
}

func (s *Service) settingsResponse(slug string, set state.APIConsumerSignupSettings) api.ConsumerSignupSettingsResponse {
	return api.ConsumerSignupSettingsResponse{App: slug, Enabled: set.Enabled, Provider: set.Provider, PlanIDs: set.PlanIDs,
		KeyScopes: set.KeyScopes, DisplayName: set.DisplayName, TermsURL: set.TermsURL, ReturnURL: set.ReturnURL,
		TrialDays: set.TrialDays, DailyLimit: set.DailyLimit, PageURL: s.PageURL(slug), CustomerPlanChanges: set.CustomerPlanChanges,
		CustomerClosing: set.CustomerClosing, CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt}
}

func invalid(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid sign-up settings", detail)
}

// settingsFromRequest applies defaults and checks a settings request.
func (s *Service) settingsFromRequest(ctx context.Context, acct state.Account, app state.App, req api.PutConsumerSignupRequest) (state.APIConsumerSignupSettings, *api.Problem) {
	set := state.APIConsumerSignupSettings{AppID: app.ID, AccountID: acct.ID, Enabled: req.Enabled == nil || *req.Enabled,
		Provider: strings.TrimSpace(req.Provider), DisplayName: strings.TrimSpace(req.DisplayName), TermsURL: strings.TrimSpace(req.TermsURL),
		ReturnURL: strings.TrimSpace(req.ReturnURL), TrialDays: req.TrialDays, DailyLimit: req.DailyLimit, KeyScopes: req.KeyScopes,
		CustomerPlanChanges: req.CustomerPlanChanges, CustomerClosing: req.CustomerClosing}
	if set.Provider == "" {
		set.Provider = state.APIConsumerSignupProviderStripe
	}
	if set.KeyScopes == nil {
		set.KeyScopes = []string{"write", api.ConsumerScopeBilling}
	}
	if set.DailyLimit == 0 {
		set.DailyLimit = api.ConsumerSignupDailyLimitDefault
	}
	switch {
	case set.Provider != state.APIConsumerSignupProviderStripe:
		return set, invalid("provider must be stripe: Paddle and Lago cannot save a card at sign-up")
	case set.DisplayName == "" || len([]rune(set.DisplayName)) > state.MaxAPIConsumerSignupNameLen:
		return set, invalid("display_name must be 1 to 80 characters")
	case set.TermsURL != "" && !api.ValidSignupURL(set.TermsURL), set.ReturnURL != "" && !api.ValidSignupURL(set.ReturnURL):
		return set, invalid("terms_url and return_url must be https URLs of at most 2048 characters")
	case set.TrialDays < 0 || set.TrialDays > state.MaxAPIConsumerSignupTrialDays:
		return set, invalid("trial_days must be 0 to 365")
	case set.DailyLimit < 1 || set.DailyLimit > api.ConsumerSignupDailyLimitMax:
		return set, invalid("daily_limit must be 1 to 1000")
	case len(set.KeyScopes) == 0 || len(set.KeyScopes) > state.MaxAPIConsumerSignupScopes:
		return set, invalid("key_scopes must name 1 to 4 scopes")
	}
	if problem := s.Host.KeyScopesProblem(acct.ID, set.KeyScopes); problem != nil {
		return set, problem
	}
	plans, ok := s.Host.Store().(state.APIConsumerPlanStore)
	for _, id := range req.PlanIDs {
		id = strings.TrimSpace(id)
		if slices.Contains(set.PlanIDs, id) {
			continue
		}
		if !ok {
			return set, invalid("consumer plans are unavailable")
		}
		if _, err := plans.GetAPIConsumerPlan(ctx, acct.ID, app.ID, id); err != nil {
			return set, invalid("plan " + id + " is not one of this app's consumer plans")
		}
		set.PlanIDs = append(set.PlanIDs, id)
	}
	if len(set.PlanIDs) > state.MaxAPIConsumerSignupPlans {
		return set, invalid("plan_ids may name at most 20 plans")
	}
	connections, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return set, api.ErrInternal("billing connections are unavailable")
	}
	if _, err := connections.GetBillingConnection(ctx, acct.ID, state.BillingProviderStripe); errors.Is(err, state.ErrNotFound) {
		return set, api.NewProblem(http.StatusConflict, api.CodeValidation, "Stripe is not connected",
			"connect Stripe first: PUT /v1/account/billing-connections/stripe")
	} else if err != nil {
		return set, api.ErrInternal("could not read the Stripe connection")
	}
	return set, nil
}

// PutSettings turns sign-up on for an app or changes its settings.
func (s *Service) PutSettings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.signupStore(w)
	if !ok || !s.Host.ConsumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	var req api.PutConsumerSignupRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	set, problem := s.settingsFromRequest(r.Context(), acct, app, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	saved, err := store.PutAPIConsumerSignupSettings(r.Context(), set)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the sign-up settings"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer.signup_settings_set", acct.ID, map[string]any{"app_id": app.ID, "enabled": saved.Enabled,
		"plan_ids": saved.PlanIDs, "key_scopes": saved.KeyScopes, "trial_days": saved.TrialDays, "daily_limit": saved.DailyLimit,
		"customer_plan_changes": saved.CustomerPlanChanges, "customer_closing": saved.CustomerClosing})
	httpkit.WriteJSON(w, http.StatusOK, s.settingsResponse(app.Slug, saved))
}

// GetSettings reads an app's sign-up settings.
func (s *Service) GetSettings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.signupStore(w)
	if !ok {
		return
	}
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	set, err := store.GetAPIConsumerSignupSettings(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "sign-up is not set up for this app")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read the sign-up settings"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, s.settingsResponse(app.Slug, set))
}

// DeleteSettings turns sign-up off. Pending sign-ups can no longer
// complete.
func (s *Service) DeleteSettings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.signupStore(w)
	if !ok {
		return
	}
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	err := store.DeleteAPIConsumerSignupSettings(r.Context(), acct.ID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "sign-up is not set up for this app")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the sign-up settings"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer.signup_settings_removed", acct.ID, map[string]any{"app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

// List returns an app's latest sign-ups.
func (s *Service) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.signupStore(w)
	if !ok {
		return
	}
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	rows, err := store.ListAPIConsumerSignups(r.Context(), acct.ID, app.ID, api.ConsumerSignupListMax)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list sign-ups"))
		return
	}
	out := api.ConsumerSignupListResponse{Signups: make([]api.ConsumerSignupResponse, 0, len(rows))}
	for _, row := range rows {
		out.Signups = append(out.Signups, api.ConsumerSignupResponse{ID: row.ID, Email: row.Email, Name: row.Name, PlanID: row.PlanID,
			Status: string(row.Status), Livemode: row.Livemode, ProviderCustomerID: row.ProviderCustomerID, ConsumerID: row.ConsumerID,
			CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, CompletedAt: row.CompletedAt})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// cleanupInterval is how often the worker deletes stale sign-ups.
const cleanupInterval = time.Hour

// Start runs the cleanup worker until ctx ends.
func (s *Service) Start(ctx context.Context, log *slog.Logger) {
	if _, ok := s.Host.Store().(state.APIConsumerSignupStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			if s.Enabled == nil || s.Enabled() {
				if _, err := s.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
					log.Warn("consumer sign-up cleanup failed", "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// CleanupOnce deletes pending sign-ups that expired more than
// ConsumerSignupRetention ago, with their email and name.
func (s *Service) CleanupOnce(ctx context.Context) (int, error) {
	store, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok {
		return 0, nil
	}
	return store.DeleteStaleAPIConsumerSignups(ctx, s.now().Add(-api.ConsumerSignupRetention))
}
