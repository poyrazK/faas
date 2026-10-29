package consumersignup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// The public sign-up API needs no credential: a customer reads the form,
// starts a sign-up that saves a card with Stripe Checkout, and completes
// it with the claim token only they received.

// requestBodyMax bounds a public request body.
const requestBodyMax = 8 << 10

func rateLimited(w http.ResponseWriter, code, detail string, limit int) {
	w.Header().Set("Retry-After", "60")
	api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, code, "Too many sign-ups", detail).WithLimit(int64(limit), int64(limit)+1))
}

func unavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeConsumerSignupUnavailable, "Sign-up is unavailable",
		"this API cannot take sign-ups right now; try again later"))
}

func noSuchSignup(w http.ResponseWriter) { httpkit.NotFound(w, "no such sign-up") }

// publicApp resolves the app of a public route. It answers like an
// unregistered route unless the owner has API monetization, and 404s
// unless sign-up is set up, and enabled when enabled is true.
func (s *Service) publicApp(w http.ResponseWriter, r *http.Request, enabled bool) (state.App, state.APIConsumerSignupSettings, bool) {
	store, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok {
		httpkit.NoSuchRoute(w)
		return state.App{}, state.APIConsumerSignupSettings{}, false
	}
	app, err := s.Host.Store().AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || !s.Host.Released(app.AccountID) {
		httpkit.NoSuchRoute(w)
		return state.App{}, state.APIConsumerSignupSettings{}, false
	}
	set, err := store.GetAPIConsumerSignupSettings(r.Context(), app.AccountID, app.ID)
	// Sign-up saves the customer's card with the Stripe account of the app's
	// creator, so a company app stops taking sign-ups once its creator left.
	if err != nil || (enabled && !set.Enabled) || !s.Host.CreatorBills(r.Context(), app) {
		httpkit.NotFound(w, "this API does not take sign-ups")
		return state.App{}, state.APIConsumerSignupSettings{}, false
	}
	return app, set, true
}

// failed tells the owner, through an audit event, why a customer could not
// sign up.
func (s *Service) failed(ctx context.Context, app state.App, signupID, reason string) {
	s.Host.Audit(ctx, "api_consumer.signup_failed", app.AccountID, map[string]any{"app_id": app.ID, "signup_id": signupID, "reason": reason})
}

// stripeFailed answers a failed Stripe call: a rejected key makes sign-up
// unavailable, a refused email is the customer's to fix, anything else is
// worth a retry.
func (s *Service) stripeFailed(ctx context.Context, w http.ResponseWriter, app state.App, signupID string, err error) {
	var stripeErr *stripeinvoice.Error
	switch {
	case errors.Is(err, stripeinvoice.ErrKeyRejected):
		s.failed(ctx, app, signupID, "Stripe rejected the key; it needs write access to Customers and Checkout Sessions and read access to SetupIntents")
		unavailable(w)
	case errors.As(err, &stripeErr) && stripeErr.Param == "email":
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid email", "the payment provider refused this email address"))
	default:
		s.failed(ctx, app, signupID, "a Stripe call failed: "+err.Error())
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeBillingProviderUnavailable, "Payment provider unavailable",
			"could not reach the payment provider; try again"))
	}
}

// Form returns what the sign-up page shows.
func (s *Service) Form(w http.ResponseWriter, r *http.Request) {
	if !s.allow(s.Host.ClientIP(r), false) {
		rateLimited(w, api.CodeConsumerSignupRateLimited, "too many sign-up requests from this address", api.ConsumerSignupReadsPerIPPerMinute)
		return
	}
	app, set, ok := s.publicApp(w, r, true)
	if !ok {
		return
	}
	out := api.ConsumerSignupFormResponse{App: app.Slug, DisplayName: set.DisplayName, TermsURL: set.TermsURL, TrialDays: set.TrialDays}
	if connections, ok := s.Host.Store().(state.StatementInvoiceExportStore); ok {
		if conn, err := connections.GetBillingConnection(r.Context(), app.AccountID, state.BillingProviderStripe); err == nil {
			out.Livemode = conn.Livemode
		}
	}
	plans, err := s.OfferedPlans(r.Context(), app, set)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read the plans"))
		return
	}
	out.Plans = plans
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// OfferedPlans lists the plans sign-up offers with each unit's current
// price; no plan IDs offer the app's default plan.
func (s *Service) OfferedPlans(ctx context.Context, app state.App, set state.APIConsumerSignupSettings) ([]api.ConsumerSignupPlan, error) {
	var cards []state.APIConsumerRateCard
	if store, ok := s.Host.Store().(state.APIConsumerRateCardStore); ok {
		var err error
		if cards, err = store.ListAPIConsumerRateCardsForApp(ctx, app.AccountID, app.ID); err != nil {
			return nil, err
		}
	}
	ids := set.PlanIDs
	if len(ids) == 0 {
		ids = []string{""}
	}
	plans, _ := s.Host.Store().(state.APIConsumerPlanStore)
	now := s.now()
	out := make([]api.ConsumerSignupPlan, 0, len(ids))
	for _, id := range ids {
		plan := api.ConsumerSignupPlan{ID: id, Name: "Standard", Prices: currentPrices(cards, id, now)}
		if id != "" && plans != nil {
			p, err := plans.GetAPIConsumerPlan(ctx, app.AccountID, app.ID, id)
			if err != nil {
				continue
			}
			plan.Name, plan.MaxRequestsPerMinute, plan.MaxUnitsPerMonth = p.Name, p.MaxRequestsPerMinute, p.MaxUnitsPerMonth
		}
		out = append(out, plan)
	}
	return out, nil
}

// currentPrices returns, per unit, the plan's card in force at now, or
// the first one to come when none is in force yet: a new plan's cards
// start no earlier than the next minute.
func currentPrices(cards []state.APIConsumerRateCard, planID string, now time.Time) []api.ConsumerSignupPrice {
	current, next := map[string]state.APIConsumerRateCard{}, map[string]state.APIConsumerRateCard{}
	for _, card := range cards {
		switch {
		case card.PlanID != planID:
		case card.EffectiveFrom.After(now):
			if prior, ok := next[card.Unit]; !ok || card.EffectiveFrom.Before(prior.EffectiveFrom) {
				next[card.Unit] = card
			}
		default:
			if prior, ok := current[card.Unit]; !ok || card.EffectiveFrom.After(prior.EffectiveFrom) {
				current[card.Unit] = card
			}
		}
	}
	for unit, card := range next {
		if _, ok := current[unit]; !ok {
			current[unit] = card
		}
	}
	out := make([]api.ConsumerSignupPrice, 0, len(current))
	for _, card := range current {
		out = append(out, api.ConsumerSignupPrice{Unit: card.Unit, Currency: card.Currency, PriceMillicentsPerUnit: card.PriceMillicentsPerUnit,
			IncludedUnitsPerMonth: card.IncludedUnitsPerMonth, Tiered: len(card.Tiers) > 0,
			BaseFeeMillicentsPerMonth: card.BaseFeeMillicentsPerMonth, MinimumMillicentsPerMonth: card.MinimumMillicentsPerMonth})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit < out[j].Unit })
	return out
}

// chosenPlan resolves the requested plan among the offered ones; with a
// single plan offered, no plan_id picks it.
func chosenPlan(set state.APIConsumerSignupSettings, requested string) (string, bool) {
	switch {
	case len(set.PlanIDs) == 0:
		return "", requested == ""
	case requested == "" && len(set.PlanIDs) == 1:
		return set.PlanIDs[0], true
	}
	return requested, slices.Contains(set.PlanIDs, requested)
}

func claimHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newClaimToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create starts a sign-up: a pending row, a Stripe customer and a
// setup-mode Checkout Session. The claim token is returned only here.
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	if !s.allow(s.Host.ClientIP(r), true) {
		rateLimited(w, api.CodeConsumerSignupRateLimited, "too many sign-ups from this address; try again later", api.ConsumerSignupPerIPPerHour)
		return
	}
	app, set, ok := s.publicApp(w, r, true)
	if !ok {
		return
	}
	ctx := r.Context()
	var req api.CreateConsumerSignupRequest
	if err := httpkit.DecodeJSONSized(r, &req, requestBodyMax); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid sign-up", err.Error()))
		return
	}
	planID, ok := chosenPlan(set, req.PlanID)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid sign-up", "plan_id must be one of the offered plans"))
		return
	}
	// A company app runs on its company's plan, not its creator's.
	acct, err := state.AppResourceAccount(ctx, s.Host.Store(), app)
	if err != nil || acct.Plan.ConsumerKeysPerApp() == 0 {
		s.failed(ctx, app, "", "the account's plan does not include API consumers")
		unavailable(w)
		return
	}
	key, livemode, problem := s.Host.StripeKey(ctx, app.AccountID)
	if problem != nil {
		s.failed(ctx, app, "", "the Stripe connection cannot be used: "+problem.Detail)
		unavailable(w)
		return
	}
	token, err := newClaimToken()
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not start the sign-up"))
		return
	}
	now := s.now()
	signups := s.Host.Store().(state.APIConsumerSignupStore)
	signup, err := signups.CreateAPIConsumerSignup(ctx, state.APIConsumerSignup{AccountID: app.AccountID, AppID: app.ID, Email: req.Email,
		Name: req.Name, PlanID: planID, Provider: state.APIConsumerSignupProviderStripe, Livemode: livemode, ClaimHash: claimHash(token),
		ExpiresAt: now.Add(api.ConsumerSignupTTL)}, now.Truncate(24*time.Hour), set.DailyLimit)
	switch {
	case errors.Is(err, state.ErrAPIConsumerSignupLimit):
		s.failed(ctx, app, "", "the app took its daily sign-ups")
		rateLimited(w, api.CodeConsumerSignupDailyLimit, "this API took all the sign-ups it accepts today; try again tomorrow", set.DailyLimit)
		return
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "this API does not take sign-ups")
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not start the sign-up"))
		return
	}
	metadata := map[string]string{"gregale_app": app.Slug, "gregale_signup": signup.ID}
	stripe := s.stripe()
	customer, err := stripe.CreateCustomer(ctx, key, "gregale-signup-"+signup.ID+"-customer", req.Email, req.Name, metadata)
	if err != nil {
		s.stripeFailed(ctx, w, app, signup.ID, err)
		return
	}
	page := s.PageURL(app.Slug) + "?signup=" + signup.ID
	session, err := stripe.CreateSetupCheckout(ctx, key, "gregale-signup-"+signup.ID+"-checkout", stripeinvoice.SetupCheckoutParams{
		Customer: customer, SuccessURL: page, CancelURL: page + "&canceled=1", ExpiresAt: now.Add(api.ConsumerSignupCheckoutTTL),
		ClientReferenceID: signup.ID, Metadata: metadata})
	if err != nil {
		s.stripeFailed(ctx, w, app, signup.ID, err)
		return
	}
	if err := signups.SetAPIConsumerSignupCheckout(ctx, app.AccountID, signup.ID, customer, session.ID); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not record the checkout"))
		return
	}
	s.Host.Audit(ctx, "api_consumer.signup_started", app.AccountID, map[string]any{"app_id": app.ID, "signup_id": signup.ID,
		"plan_id": planID, "livemode": livemode})
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusCreated, api.CreateConsumerSignupResponse{ID: signup.ID, CheckoutURL: session.URL, ClaimToken: token,
		ExpiresAt: signup.ExpiresAt})
}

// Complete creates the sign-up's consumer once Stripe saved the card and
// returns a new key; a later call issues another key and revokes the last.
func (s *Service) Complete(w http.ResponseWriter, r *http.Request) {
	if !s.allow(s.Host.ClientIP(r), false) {
		rateLimited(w, api.CodeConsumerSignupRateLimited, "too many sign-up requests from this address", api.ConsumerSignupReadsPerIPPerMinute)
		return
	}
	app, set, ok := s.publicApp(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	var req api.CompleteConsumerSignupRequest
	if err := httpkit.DecodeJSONSized(r, &req, requestBodyMax); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	signups := s.Host.Store().(state.APIConsumerSignupStore)
	signup, err := signups.GetAPIConsumerSignup(ctx, app.AccountID, r.PathValue("signup"))
	if err != nil || signup.AppID != app.ID || subtle.ConstantTimeCompare(claimHash(req.ClaimToken), signup.ClaimHash) != 1 {
		noSuchSignup(w)
		return
	}
	now := s.now()
	if !now.Before(signup.ExpiresAt) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeConsumerSignupExpired, "Sign-up expired",
			"sign-ups complete within 24 hours; start again, or ask the API owner for a new key"))
		return
	}
	acct, err := state.AppResourceAccount(ctx, s.Host.Store(), app)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not complete the sign-up"))
		return
	}
	consumer, ok := s.completedConsumer(ctx, w, app, set, signup, now)
	if !ok {
		return
	}
	s.issueKey(ctx, w, acct, app, set, signup, consumer)
}

// completedConsumer returns the sign-up's consumer, creating it once Stripe
// reports the card saved.
func (s *Service) completedConsumer(ctx context.Context, w http.ResponseWriter, app state.App, set state.APIConsumerSignupSettings,
	signup state.APIConsumerSignup, now time.Time) (state.APIConsumer, bool) {
	if signup.Status == state.APIConsumerSignupCompleted {
		consumer, err := s.Host.Store().GetAPIConsumerByID(ctx, app.AccountID, signup.ConsumerID)
		if err != nil || !consumer.Active() {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Customer revoked", "the API owner revoked this customer"))
			return state.APIConsumer{}, false
		}
		return consumer, true
	}
	key, _, problem := s.Host.StripeKey(ctx, app.AccountID)
	if problem != nil {
		s.failed(ctx, app, signup.ID, "the Stripe connection cannot be used: "+problem.Detail)
		unavailable(w)
		return state.APIConsumer{}, false
	}
	stripe := s.stripe()
	session, err := stripe.GetCheckoutSession(ctx, key, signup.CheckoutSessionID)
	if err != nil {
		s.stripeFailed(ctx, w, app, signup.ID, err)
		return state.APIConsumer{}, false
	}
	if !session.Complete() || session.Customer != signup.ProviderCustomerID {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConsumerSignupCheckoutIncomplete, "Card not saved yet",
			"finish the checkout to save a card, then complete the sign-up"))
		return state.APIConsumer{}, false
	}
	if err := stripe.SetDefaultPaymentMethod(ctx, key, signup.ProviderCustomerID, session.SetupIntent.PaymentMethod); err != nil {
		s.stripeFailed(ctx, w, app, signup.ID, err)
		return state.APIConsumer{}, false
	}
	signups := s.Host.Store().(state.APIConsumerSignupStore)
	consumer, created, err := signups.CompleteAPIConsumerSignup(ctx, state.APIConsumerSignupCompletion{AccountID: app.AccountID,
		SignupID: signup.ID, EffectiveFrom: now.Truncate(time.Minute), TrialDays: set.TrialDays})
	if err != nil {
		s.failed(ctx, app, signup.ID, "the consumer could not be created: "+err.Error())
		api.WriteProblem(w, api.ErrInternal("could not complete the sign-up"))
		return state.APIConsumer{}, false
	}
	if created {
		if outbox, ok := s.Host.Store().(state.AppWebhookEventOutboxStore); ok {
			_, _ = outbox.RelayAppWebhookEventOutboxSource(ctx, state.AppWebhookEventConsumerSignedUp, signup.ID)
		}
		s.Host.Audit(ctx, "api_consumer.created", app.AccountID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID,
			"external_ref": consumer.ExternalRef, "signup_id": signup.ID})
		s.Host.Audit(ctx, "api_consumer.signed_up", app.AccountID, map[string]any{"app_id": app.ID, "consumer_id": consumer.ID,
			"signup_id": signup.ID, "plan_id": signup.PlanID, "trial_days": set.TrialDays, "livemode": signup.Livemode})
	}
	return consumer, true
}

// issueKey issues the sign-up's key, revokes the one it issued before and
// writes the response.
func (s *Service) issueKey(ctx context.Context, w http.ResponseWriter, acct state.Account, app state.App, set state.APIConsumerSignupSettings,
	signup state.APIConsumerSignup, consumer state.APIConsumer) {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	key, plaintext, problem := s.Host.IssueConsumerKey(ctx, acct, consumer, "signup-"+hex.EncodeToString(suffix), set.KeyScopes)
	if problem != nil {
		s.failed(ctx, app, signup.ID, "the key could not be issued: "+problem.Detail)
		unavailable(w)
		return
	}
	signups := s.Host.Store().(state.APIConsumerSignupStore)
	previous, err := signups.SetAPIConsumerSignupKey(ctx, app.AccountID, signup.ID, key.ID)
	if err != nil {
		_, _ = s.Host.Store().RevokeConsumerKey(ctx, app.AccountID, key.ID)
		api.WriteProblem(w, api.ErrInternal("could not record the key"))
		return
	}
	if previous != "" && previous != key.ID {
		_, _ = s.Host.Store().RevokeConsumerKey(ctx, app.AccountID, previous)
	}
	out := api.CompleteConsumerSignupResponse{ConsumerID: consumer.ID, Key: plaintext, KeyPrefix: key.Prefix, Scopes: key.Scopes,
		PlanID: signup.PlanID, ReturnURL: set.ReturnURL}
	if trials, ok := s.Host.Store().(state.APIConsumerTrialStore); ok {
		if trial, err := trials.GetAPIConsumerTrial(ctx, app.AccountID, app.ID, consumer.ID); err == nil {
			out.TrialEndsAt = &trial.EndsAt
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, out)
}
