package consumersignup

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Card-less free plan (ADR-20261124094745): one offered plan whose rate
// cards charge nothing can be joined without a Stripe Checkout.

// rateCardChargesNothing reports whether a card bills nothing at any usage.
func rateCardChargesNothing(card state.APIConsumerRateCard) bool {
	if card.PriceMillicentsPerUnit != 0 || card.BaseFeeMillicentsPerMonth != 0 || card.MinimumMillicentsPerMonth != 0 {
		return false
	}
	return !slices.ContainsFunc(card.Tiers, func(t state.APIConsumerRateCardTier) bool { return t.PriceMillicentsPerUnit != 0 })
}

// PlanChargesNothing reports whether every rate card of a plan that is in
// force at now, or starts later, charges nothing, and at least one is.
func PlanChargesNothing(cards []state.APIConsumerRateCard, planID string, now time.Time) bool {
	current := map[string]state.APIConsumerRateCard{}
	var any bool
	for _, card := range cards {
		if card.PlanID != planID {
			continue
		}
		if card.EffectiveFrom.After(now) {
			if !rateCardChargesNothing(card) {
				return false
			}
			any = true
			continue
		}
		if prior, ok := current[card.Unit]; !ok || card.EffectiveFrom.After(prior.EffectiveFrom) {
			current[card.Unit] = card
		}
	}
	for _, card := range current {
		if !rateCardChargesNothing(card) {
			return false
		}
		any = true
	}
	return any
}

// freePlanProblem checks a settings request's free plan and OAuth consumers.
func (s *Service) freePlanProblem(ctx context.Context, app state.App, set state.APIConsumerSignupSettings) *api.Problem {
	if set.FreePlanID == "" {
		if set.OAuthConsumers {
			return invalid("oauth_consumers needs free_plan_id: OAuth callers join the free plan")
		}
		return nil
	}
	if !slices.Contains(set.PlanIDs, set.FreePlanID) {
		return invalid("free_plan_id must be one of plan_ids")
	}
	store, ok := s.Host.Store().(state.APIConsumerRateCardStore)
	if !ok {
		return api.ErrInternal("rate cards are unavailable")
	}
	cards, err := store.ListAPIConsumerRateCardsForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return api.ErrInternal("could not read the rate cards")
	}
	if !PlanChargesNothing(cards, set.FreePlanID, s.now()) {
		return invalid("free_plan_id must be a plan whose rate cards charge nothing: no unit price, priced tier, base fee or minimum " +
			"(gregale consumers rate-card-create APP --plan PLAN --currency USD --price-millicents 0)")
	}
	return nil
}

// FreePlanCardProblem refuses a rate card that charges on the app's sign-up
// free plan, so joining it without a card stays free.
func (s *Service) FreePlanCardProblem(ctx context.Context, app state.App, card state.APIConsumerRateCard) *api.Problem {
	if card.PlanID == "" || rateCardChargesNothing(card) {
		return nil
	}
	signups, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok {
		return nil
	}
	set, err := signups.GetAPIConsumerSignupSettings(ctx, app.AccountID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return api.ErrInternal("could not read the sign-up settings")
	}
	if set.FreePlanID != card.PlanID {
		return nil
	}
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid rate card",
		"this plan is the sign-up free plan, joined without a card, so its rate cards must charge nothing; "+
			"pick another free plan first (gregale api consumer signup set APP --free-plan NAME)")
}

// completeFree creates a free-plan sign-up's consumer without a checkout.
func (s *Service) completeFree(ctx context.Context, w http.ResponseWriter, app state.App, signup state.APIConsumerSignup, now time.Time) (state.APIConsumer, bool) {
	signups := s.Host.Store().(state.APIConsumerSignupStore)
	consumer, created, err := signups.CompleteAPIConsumerSignup(ctx, state.APIConsumerSignupCompletion{AccountID: app.AccountID,
		SignupID: signup.ID, EffectiveFrom: now.Truncate(time.Minute), Free: true})
	if err != nil {
		s.failed(ctx, app, signup.ID, "the consumer could not be created: "+err.Error())
		api.WriteProblem(w, api.ErrInternal("could not complete the sign-up"))
		return state.APIConsumer{}, false
	}
	if created {
		s.signedUp(ctx, app, signup, consumer, 0)
	}
	return consumer, true
}
