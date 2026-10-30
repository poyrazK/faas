package consumerbilling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Customer self-service (ADR-20261021183000): with a billing-scope key a
// customer opens Stripe's customer portal to update their card, switches
// between the plans the owner offers at sign-up, or closes their account,
// as far as the owner's sign-up settings allow.

func selfServiceUnavailable(w http.ResponseWriter, detail string) {
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConsumerSelfServiceUnavailable, "Not available", detail))
}

func (s *Service) stripe() *stripeinvoice.Client {
	if s.Stripe != nil {
		return s.Stripe
	}
	return stripeinvoice.New()
}

// signupSettings reads the app's sign-up settings, which say what
// customers may do on their own; ok is false when there are none.
func (s *Service) signupSettings(ctx context.Context, app state.App) (state.APIConsumerSignupSettings, bool) {
	store, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok {
		return state.APIConsumerSignupSettings{}, false
	}
	set, err := store.GetAPIConsumerSignupSettings(ctx, app.AccountID, app.ID)
	return set, err == nil
}

func (s *Service) currentPlanID(ctx context.Context, c caller) string {
	plans, ok := s.Host.Store().(state.APIConsumerPlanStore)
	if !ok {
		return ""
	}
	policy, err := plans.GetAPIConsumerPlanPolicy(ctx, c.app.AccountID, c.app.ID, c.consumer.ID, s.now())
	if err != nil {
		return ""
	}
	return policy.PlanID
}

// relay delivers a committed webhook event now; the outbox drain retries
// on failure.
func (s *Service) relay(ctx context.Context, event state.AppWebhookEvent, sourceID string) {
	if outbox, ok := s.Host.Store().(state.AppWebhookEventOutboxStore); ok {
		_, _ = outbox.RelayAppWebhookEventOutboxSource(ctx, event, sourceID)
	}
}

// Plans lists the plans the customer can switch between and what they may
// do on their own.
func (s *Service) Plans(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	out := api.ConsumerBillingPlansResponse{CurrentPlanID: s.currentPlanID(r.Context(), c), Plans: []api.ConsumerSignupPlan{}}
	if set, ok := s.signupSettings(r.Context(), c.app); ok {
		plans, err := s.Host.OfferedPlans(r.Context(), c.app, set)
		if err != nil {
			api.WriteProblem(w, api.ErrInternal("could not read the plans"))
			return
		}
		out.Plans, out.CanChange, out.CanClose = plans, set.CustomerPlanChanges && len(set.PlanIDs) > 0, set.CustomerClosing
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// ChangePlan switches the customer to an offered plan from the next
// minute.
func (s *Service) ChangePlan(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req api.ChangeConsumerBillingPlanRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	ctx := r.Context()
	set, ok := s.signupSettings(ctx, c.app)
	if !ok || !set.CustomerPlanChanges || len(set.PlanIDs) == 0 {
		selfServiceUnavailable(w, "this API does not let customers change plans; ask the API owner")
		return
	}
	target, current := strings.TrimSpace(req.PlanID), s.currentPlanID(ctx, c)
	if !slices.Contains(set.PlanIDs, target) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid plan", "plan_id must be one of the offered plans"))
		return
	}
	if target == current {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Already on this plan", "the consumer is already on this plan"))
		return
	}
	self, ok := s.Host.Store().(state.APIConsumerSelfServiceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("plan changes are unavailable"))
		return
	}
	from := s.now().Truncate(time.Minute).Add(time.Minute)
	assignment, err := self.ChangeAPIConsumerPlanSelf(ctx, state.APIConsumerPlanAssignment{AccountID: c.app.AccountID, AppID: c.app.ID,
		ConsumerID: c.consumer.ID, PlanID: target, EffectiveFrom: from}, current)
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Plan change pending", "a plan change starts at the next minute already; try again in a minute"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not change the plan"))
		return
	}
	s.relay(ctx, state.AppWebhookEventConsumerPlanChanged, assignment.ID)
	s.Host.Audit(ctx, "api_consumer.plan_changed_by_customer", c.app.AccountID, map[string]any{"app_id": c.app.ID, "consumer_id": c.consumer.ID,
		"from_plan_id": current, "to_plan_id": target, "effective_from": from})
	httpkit.WriteJSON(w, http.StatusOK, api.ConsumerBillingPlanChangeResponse{PlanID: assignment.PlanID, EffectiveFrom: assignment.EffectiveFrom})
}

// Portal returns a Stripe customer portal URL for the customer's Stripe
// customer, their external ref.
func (s *Service) Portal(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req api.CreateConsumerBillingPortalRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	ctx := r.Context()
	if !strings.HasPrefix(c.consumer.ExternalRef, "cus_") {
		selfServiceUnavailable(w, "this account is not billed through Stripe; ask the API owner")
		return
	}
	// The portal opens the customer in the Stripe account of the app's
	// creator, which a company app stops using once its creator left.
	if !s.Host.CreatorBills(ctx, c.app) {
		selfServiceUnavailable(w, "the billing portal is not available right now; ask the API owner")
		return
	}
	returnURL := strings.TrimSpace(req.ReturnURL)
	if returnURL != "" && !api.ValidSignupURL(returnURL) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid return URL", "return_url must be an https URL"))
		return
	}
	if set, ok := s.signupSettings(ctx, c.app); ok && returnURL == "" {
		returnURL = set.ReturnURL
	}
	key, _, problem := s.Host.StripeKey(ctx, c.app.AccountID)
	if problem != nil {
		s.portalFailed(ctx, c, "the Stripe connection cannot be used: "+problem.Detail)
		selfServiceUnavailable(w, "the billing portal is not available right now; ask the API owner")
		return
	}
	url, err := s.stripe().CreatePortalSession(ctx, key, c.consumer.ExternalRef, returnURL)
	var stripeErr *stripeinvoice.Error
	switch {
	case errors.Is(err, stripeinvoice.ErrKeyRejected), errors.As(err, &stripeErr) && stripeErr.Status < 500:
		s.portalFailed(ctx, c, "Stripe refused the portal session: "+err.Error())
		selfServiceUnavailable(w, "the billing portal is not available right now; ask the API owner")
		return
	case err != nil:
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeBillingProviderUnavailable, "Payment provider unavailable",
			"could not reach the payment provider; try again"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, api.ConsumerBillingPortalResponse{URL: url})
}

// portalFailed tells the owner, through an audit event, why a customer
// could not open the portal: often the portal is not saved in Stripe yet.
func (s *Service) portalFailed(ctx context.Context, c caller, reason string) {
	s.Host.Audit(ctx, "api_consumer.portal_failed", c.app.AccountID, map[string]any{"app_id": c.app.ID, "consumer_id": c.consumer.ID, "reason": reason})
}

// Close closes the customer's account: the consumer is revoked, so its
// keys stop working; usage until then is still billed.
func (s *Service) Close(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if set, ok := s.signupSettings(ctx, c.app); !ok || !set.CustomerClosing {
		selfServiceUnavailable(w, "this API does not let customers close their account; ask the API owner")
		return
	}
	self, ok := s.Host.Store().(state.APIConsumerSelfServiceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("closing is unavailable"))
		return
	}
	consumer, changed, err := self.CloseAPIConsumerSelf(ctx, c.app.AccountID, c.consumer.ID)
	if err != nil || consumer.RevokedAt == nil {
		api.WriteProblem(w, api.ErrInternal("could not close the account"))
		return
	}
	if changed {
		s.relay(ctx, state.AppWebhookEventConsumerClosed, consumer.ID)
		s.Host.Audit(ctx, "api_consumer.closed_by_customer", c.app.AccountID, map[string]any{"app_id": c.app.ID, "consumer_id": consumer.ID})
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ConsumerBillingCloseResponse{ConsumerID: consumer.ID, ClosedAt: *consumer.RevokedAt})
}
