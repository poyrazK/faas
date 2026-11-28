package consumersignup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	for _, price := range card.ModelPrices {
		if price != 0 {
			return false
		}
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

// offersOnlyFreePlan reports whether sign-up offers the free plan and nothing
// else. No plan IDs offer the app's default plan, which is not the free plan.
func offersOnlyFreePlan(set state.APIConsumerSignupSettings) bool {
	return set.FreePlanID != "" && len(set.PlanIDs) == 1 && set.PlanIDs[0] == set.FreePlanID
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

// oauthGatewayProblem refuses OAuth consumers on an app whose gateway does
// not check MCP OAuth tokens: only an enabled jwt rule with an MCP OAuth
// resource policy tells the gateway who calls, so without one callers are
// served but never become customers or metered (ADR-20261124094745).
//
// It applies only when enabled OAuth consumers are being turned on, so a
// seller whose settings predate the check can still edit or pause them.
func (s *Service) oauthGatewayProblem(ctx context.Context, app state.App, set state.APIConsumerSignupSettings) *api.Problem {
	if !set.OAuthConsumers || !set.Enabled {
		return nil
	}
	if signups, ok := s.Host.Store().(state.APIConsumerSignupStore); ok {
		prior, err := signups.GetAPIConsumerSignupSettings(ctx, app.AccountID, app.ID)
		if err == nil && prior.OAuthConsumers && prior.Enabled {
			return nil
		}
	}
	rules, err := s.Host.Store().ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		slog.ErrorContext(ctx, "consumer sign-up: list edge rules for the OAuth consumer check", "app_id", app.ID, "err", err)
		return api.ErrInternal("could not read the app's gateway rules")
	}
	now := s.now()
	if slices.ContainsFunc(rules, func(rule state.EdgeRule) bool { return mcpOAuthGatewayRule(rule, now) }) {
		return nil
	}
	return api.NewProblem(http.StatusConflict, api.CodeMCPGatewayPolicyMissing, "Gateway does not check this server's tokens",
		"oauth_consumers needs Gregale to check the MCP server's OAuth tokens; deploy it with gregale mcp server deploy, "+
			"or run gregale mcp policy --path DIR --name "+app.Slug)
}

// mcpOAuthGatewayRule reports whether rule checks MCP OAuth tokens at the
// gateway for every endpoint request: an enabled, unexpired jwt rule on /**
// with no method or header conditions, whose MCP policy is not consumer
// keys (the shape `gregale mcp policy` stores).
func mcpOAuthGatewayRule(rule state.EdgeRule, now time.Time) bool {
	jwt := rule.Action.JWT
	return rule.Enabled && rule.Kind == state.EdgeRuleKindJWT && jwt != nil && jwt.MCP != nil && !jwt.MCP.ConsumerKey() &&
		(rule.ExpiresAt == nil || rule.ExpiresAt.After(now)) && rule.MatchPath == "/**" &&
		len(rule.MatchMethods) == 0 && len(rule.MatchHeaders) == 0
}

// FreePlanCardProblem refuses a rate card that charges on the app's sign-up
// free plan, so joining it without a card stays free, or on any plan
// card-less consumers are on (a former free plan), whose charges could not
// be collected.
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
	if set.FreePlanID == card.PlanID {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid rate card",
			"this plan is the sign-up free plan, joined without a card, so its rate cards must charge nothing; "+
				"pick another free plan first (gregale api consumer signup set APP --free-plan NAME)")
	}
	return s.CardlessPlanProblem(ctx, app, card.PlanID)
}

// PlanChargesNothingNow reports whether planID charges nothing now.
func (s *Service) PlanChargesNothingNow(ctx context.Context, app state.App, planID string) bool {
	return PlanChargesNothingIn(ctx, s.Host.Store(), app, planID, s.now())
}

// PlanChargesNothingIn reports whether planID charges nothing at now,
// reading the app's rate cards from store; false when they cannot be read.
func PlanChargesNothingIn(ctx context.Context, store any, app state.App, planID string, now time.Time) bool {
	cardsStore, ok := store.(state.APIConsumerRateCardStore)
	if !ok {
		return false
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(ctx, app.AccountID, app.ID)
	return err == nil && PlanChargesNothing(cards, planID, now)
}

// CardlessPlanProblem refuses charging on planID while card-less consumers
// are on it.
func (s *Service) CardlessPlanProblem(ctx context.Context, app state.App, planID string) *api.Problem {
	upgrades, ok := s.Host.Store().(state.APIConsumerUpgradeStore)
	if !ok || planID == "" {
		return nil
	}
	n, err := upgrades.CountCardlessAPIConsumersOnPlan(ctx, app.AccountID, app.ID, planID, s.now())
	if err != nil {
		return api.ErrInternal("could not check the plan's consumers")
	}
	if n == 0 {
		return nil
	}
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid rate card",
		fmt.Sprintf("%d consumers on this plan joined without a card, so its rate cards must charge nothing; "+
			"move them to a free plan first, or let them upgrade through the upgrade link", n)).WithLimit(0, int64(n))
}

// CardlessAssignmentProblem refuses moving a card-less consumer onto a
// plan that charges: it must save a card through the upgrade page first.
func (s *Service) CardlessAssignmentProblem(ctx context.Context, app state.App, consumer state.APIConsumer, planID string) *api.Problem {
	if !state.APIConsumerCardless(consumer.ExternalRef) {
		return nil
	}
	cardsStore, ok := s.Host.Store().(state.APIConsumerRateCardStore)
	if !ok {
		return nil
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return api.ErrInternal("could not read the rate cards")
	}
	if PlanChargesNothing(cards, planID, s.now()) {
		return nil
	}
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid plan assignment",
		"this consumer joined without a card, so it can only be on a plan that charges nothing; "+
			"it moves to a paid plan by saving a card through its upgrade link")
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

// liveMode is the mode a sign-up records and the hosted pages show: the
// Stripe connection's. Without a connection a seller can offer only the
// card-less free plan, so no sign-up is a test: live.
func (s *Service) liveMode(ctx context.Context, accountID string) bool {
	connections, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return false
	}
	conn, err := connections.GetBillingConnection(ctx, accountID, state.BillingProviderStripe)
	switch {
	case err == nil:
		return conn.Livemode
	case errors.Is(err, state.ErrNotFound):
		return true
	}
	return false
}
