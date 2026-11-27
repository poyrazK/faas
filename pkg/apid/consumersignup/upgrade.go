package consumersignup

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Upgrade links (ADR-20261124094745). A consumer that reached its plan's
// limit gets a link to a hosted page where it moves to another offered
// plan, saving a card with Stripe Checkout first when the plan charges and
// it has none. The link's token names the consumer and app and expires; it
// opens only that page and grants no API access. A second token, minted
// when Checkout starts, binds the plan and the new Stripe customer, so the
// page cannot attach a customer apid did not create for this consumer.

const (
	upgradeTokenPrefix  = "cup1"
	completeTokenPrefix = "cuc1"
	upgradeKeyInfo      = "gregale consumer upgrade v1"
)

// errUpgradeToken is a malformed, forged or expired token.
var errUpgradeToken = errors.New("consumersignup: invalid upgrade token")

// UpgradeKey derives the upgrade-link key from secret, apid's session
// binding key, so every apid replica verifies every replica's links.
func UpgradeKey(secret []byte) ([]byte, error) {
	if len(secret) < 32 {
		return nil, errors.New("consumersignup: upgrade signing secret must be at least 32 bytes")
	}
	return hkdf.Key(sha256.New, secret, nil, upgradeKeyInfo, 32)
}

type upgradeClaims struct {
	AppID      string `json:"p"`
	ConsumerID string `json:"c"`
	PlanID     string `json:"l,omitempty"`
	Customer   string `json:"s,omitempty"`
	Expires    int64  `json:"x"`
}

func (s *Service) signUpgrade(prefix string, c upgradeClaims) (string, error) {
	if len(s.UpgradeKey) == 0 {
		return "", errors.New("consumersignup: no upgrade key")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := prefix + "." + base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.UpgradeKey)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Service) verifyUpgrade(prefix, token string, now time.Time) (upgradeClaims, error) {
	parts := strings.Split(token, ".")
	if len(s.UpgradeKey) == 0 || len(parts) != 3 || parts[0] != prefix {
		return upgradeClaims{}, errUpgradeToken
	}
	mac := hmac.New(sha256.New, s.UpgradeKey)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return upgradeClaims{}, errUpgradeToken
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	var c upgradeClaims
	if err != nil || json.Unmarshal(body, &c) != nil || c.AppID == "" || c.ConsumerID == "" || now.Unix() >= c.Expires {
		return upgradeClaims{}, errUpgradeToken
	}
	return c, nil
}

// upgradeOffered reports whether set lets customers move between plans.
func upgradeOffered(set state.APIConsumerSignupSettings) bool {
	return set.Enabled && set.CustomerPlanChanges && len(set.PlanIDs) > 1
}

// UpgradeLink returns the upgrade page URL for a consumer of app, or ""
// when the app does not let customers change plans or the consumer is not
// its active customer.
func (s *Service) UpgradeLink(ctx context.Context, accountID, appID, consumerID string) (string, time.Time, error) {
	signups, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok || len(s.UpgradeKey) == 0 || !s.Host.Released(accountID) {
		return "", time.Time{}, nil
	}
	app, err := s.Host.Store().AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && app.AccountID != accountID) {
		return "", time.Time{}, state.ErrNotFound
	}
	if err != nil {
		return "", time.Time{}, err
	}
	set, err := signups.GetAPIConsumerSignupSettings(ctx, accountID, appID)
	if errors.Is(err, state.ErrNotFound) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if !upgradeOffered(set) || !s.Host.CreatorBills(ctx, app) {
		return "", time.Time{}, nil
	}
	consumer, err := s.Host.Store().GetAPIConsumerByID(ctx, accountID, consumerID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (consumer.AppID != appID || !consumer.Active())) {
		return "", time.Time{}, nil
	}
	if err == nil && !state.APIConsumerCardless(consumer.ExternalRef) && !state.APIConsumerHasStripeCustomer(consumer.ExternalRef) {
		// The owner bills this consumer some other way: no self-serve page.
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().Add(api.ConsumerUpgradeLinkTTL)
	token, err := s.signUpgrade(upgradeTokenPrefix, upgradeClaims{AppID: appID, ConsumerID: consumerID, Expires: expires.Unix()})
	if err != nil {
		return "", time.Time{}, err
	}
	return s.upgradePageURL(app.Slug) + "?token=" + url.QueryEscape(token), expires, nil
}

func (s *Service) upgradePageURL(slug string) string {
	return s.Host.PublicOrigin() + "/v1/consumer-signup/" + url.PathEscape(slug) + "/upgrade"
}

// upgradeCaller is an upgrade request's app, settings and consumer.
type upgradeCaller struct {
	app      state.App
	set      state.APIConsumerSignupSettings
	consumer state.APIConsumer
}

// upgradeCaller verifies token for the route's app as of at. On false it
// has written the problem.
func (s *Service) upgradeCaller(w http.ResponseWriter, r *http.Request, token string, at time.Time) (upgradeCaller, bool) {
	if !s.allow(s.Host.ClientIP(r), false) {
		rateLimited(w, api.CodeConsumerSignupRateLimited, "too many requests from this address", api.ConsumerSignupReadsPerIPPerMinute)
		return upgradeCaller{}, false
	}
	app, set, ok := s.publicApp(w, r, true)
	if !ok {
		return upgradeCaller{}, false
	}
	claims, err := s.verifyUpgrade(upgradeTokenPrefix, token, at)
	if err != nil || claims.AppID != app.ID {
		upgradeLinkInvalid(w)
		return upgradeCaller{}, false
	}
	if !upgradeOffered(set) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConsumerSelfServiceUnavailable, "Plan changes unavailable",
			"this API does not let customers change plans; ask the API owner"))
		return upgradeCaller{}, false
	}
	consumer, err := s.Host.Store().GetAPIConsumerByID(r.Context(), app.AccountID, claims.ConsumerID)
	if err != nil || consumer.AppID != app.ID || !consumer.Active() {
		upgradeLinkInvalid(w)
		return upgradeCaller{}, false
	}
	return upgradeCaller{app: app, set: set, consumer: consumer}, true
}

func upgradeLinkInvalid(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Upgrade link invalid",
		"this upgrade link is invalid or expired; call the API again for a new one"))
}

// currentPlanID is the consumer's plan now; "" is the app's default plan.
func (s *Service) currentPlanID(ctx context.Context, c upgradeCaller) string {
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

// Upgrade answers the upgrade page with the customer's plan and the plans
// it can move to.
func (s *Service) Upgrade(w http.ResponseWriter, r *http.Request) {
	c, ok := s.upgradeCaller(w, r, r.URL.Query().Get("token"), s.now())
	if !ok {
		return
	}
	plans, err := s.OfferedPlans(r.Context(), c.app, c.set)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read the plans"))
		return
	}
	livemode := s.liveMode(r.Context(), c.app.AccountID)
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, api.ConsumerUpgradeResponse{App: c.app.Slug, DisplayName: c.set.DisplayName, Livemode: livemode,
		CurrentPlanID: s.currentPlanID(r.Context(), c), Plans: plans, HasCard: state.APIConsumerHasStripeCustomer(c.consumer.ExternalRef)})
}

// StartUpgrade moves the customer to an offered plan, or starts a Stripe
// Checkout to save a card first when the plan charges and it has none.
func (s *Service) StartUpgrade(w http.ResponseWriter, r *http.Request) {
	var req api.StartConsumerUpgradeRequest
	if err := httpkit.DecodeJSONSized(r, &req, requestBodyMax); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	c, ok := s.upgradeCaller(w, r, req.Token, s.now())
	if !ok {
		return
	}
	ctx := r.Context()
	target := strings.TrimSpace(req.PlanID)
	if !slices.Contains(c.set.PlanIDs, target) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid plan", "plan_id must be one of the offered plans"))
		return
	}
	if target == c.set.FreePlanID || state.APIConsumerHasStripeCustomer(c.consumer.ExternalRef) || s.PlanChargesNothingNow(ctx, c.app, target) {
		s.changePlan(ctx, w, c, target)
		return
	}
	if !state.APIConsumerCardless(c.consumer.ExternalRef) {
		// The owner bills this consumer some other way.
		billedElsewhere(w)
		return
	}
	result, ok := s.startCheckout(ctx, w, c, target, req.Token)
	if !ok {
		return
	}
	s.Host.Audit(ctx, "api_consumer.upgrade_started", c.app.AccountID, map[string]any{"app_id": c.app.ID, "consumer_id": c.consumer.ID,
		"plan_id": target})
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, result)
}

// startCheckout creates c's Stripe customer and the setup Checkout that
// saves its card, returning to the upgrade page with a complete token. On
// false it has written the problem.
func (s *Service) startCheckout(ctx context.Context, w http.ResponseWriter, c upgradeCaller, target, token string) (api.ConsumerUpgradeResult, bool) {
	key, _, problem := s.Host.StripeKey(ctx, c.app.AccountID)
	if problem != nil {
		s.failed(ctx, c.app, "", "the Stripe connection cannot be used: "+problem.Detail)
		unavailable(w)
		return api.ConsumerUpgradeResult{}, false
	}
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	attempt := "gregale-upgrade-" + c.consumer.ID + "-" + hex.EncodeToString(nonce)
	metadata := map[string]string{"gregale_app": c.app.Slug, "gregale_consumer": c.consumer.ID}
	stripe := s.stripe()
	// An OAuth consumer's name may be its token's email claim, which no one
	// verified, so Stripe gets an email only from a sign-up, which typed it.
	customer, err := stripe.CreateCustomer(ctx, key, attempt+"-customer", s.signupEmail(ctx, c), c.consumer.Name, metadata)
	if err != nil {
		s.stripeFailed(ctx, w, c.app, "", err)
		return api.ConsumerUpgradeResult{}, false
	}
	now := s.now()
	complete, err := s.signUpgrade(completeTokenPrefix, upgradeClaims{AppID: c.app.ID, ConsumerID: c.consumer.ID, PlanID: target,
		Customer: customer, Expires: now.Add(api.ConsumerSignupTTL).Unix()})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not start the upgrade"))
		return api.ConsumerUpgradeResult{}, false
	}
	page := s.upgradePageURL(c.app.Slug) + "?token=" + url.QueryEscape(token)
	session, err := stripe.CreateSetupCheckout(ctx, key, attempt+"-checkout", stripeinvoice.SetupCheckoutParams{
		Customer: customer, SuccessURL: page + "&complete=" + url.QueryEscape(complete) + "&session={CHECKOUT_SESSION_ID}",
		CancelURL: page + "&canceled=1", ExpiresAt: now.Add(api.ConsumerSignupCheckoutTTL), ClientReferenceID: c.consumer.ID, Metadata: metadata})
	if err != nil {
		s.stripeFailed(ctx, w, c.app, "", err)
		return api.ConsumerUpgradeResult{}, false
	}
	return api.ConsumerUpgradeResult{PlanID: target, CheckoutURL: session.URL, CompleteToken: complete}, true
}

// signupEmail is the email a free-plan sign-up typed for c's consumer, or
// "" for any other consumer.
func (s *Service) signupEmail(ctx context.Context, c upgradeCaller) string {
	id, ok := strings.CutPrefix(c.consumer.ExternalRef, "signup:")
	signups, isStore := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok || !isStore {
		return ""
	}
	signup, err := signups.GetAPIConsumerSignup(ctx, c.app.AccountID, id)
	if err != nil || signup.AppID != c.app.ID {
		return ""
	}
	return signup.Email
}

// billedElsewhere answers a consumer the owner bills outside Gregale's
// Stripe customers, or one another upgrade already attached.
func billedElsewhere(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Account already has a card",
		"this account is billed through another payment account; ask the API owner"))
}

// CompleteUpgrade attaches the Stripe customer whose card Checkout saved
// and moves the customer to the plan it chose.
func (s *Service) CompleteUpgrade(w http.ResponseWriter, r *http.Request) {
	var req api.CompleteConsumerUpgradeRequest
	if err := httpkit.DecodeJSONSized(r, &req, requestBodyMax); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	claims, err := s.verifyUpgrade(completeTokenPrefix, req.CompleteToken, s.now())
	if err != nil {
		upgradeLinkInvalid(w)
		return
	}
	// The upgrade link only had to be valid when the checkout started: a
	// customer who saves the card after it expires still finishes.
	started := time.Unix(claims.Expires, 0).Add(-api.ConsumerSignupTTL)
	c, ok := s.upgradeCaller(w, r, req.Token, started)
	if !ok {
		return
	}
	if claims.AppID != c.app.ID || claims.ConsumerID != c.consumer.ID || !slices.Contains(c.set.PlanIDs, claims.PlanID) {
		upgradeLinkInvalid(w)
		return
	}
	ctx := r.Context()
	consumer, ok := s.saveCheckoutCard(ctx, w, c, claims, req.CheckoutSessionID)
	if !ok {
		return
	}
	s.Host.Audit(ctx, "api_consumer.card_saved", c.app.AccountID, map[string]any{"app_id": c.app.ID, "consumer_id": consumer.ID,
		"external_ref": consumer.ExternalRef})
	c.consumer = consumer
	s.changePlan(ctx, w, c, claims.PlanID)
}

// saveCheckoutCard checks that the checkout saved a card for the complete
// token's customer, makes it the default and attaches the customer to c's
// consumer. On false it has written the problem.
func (s *Service) saveCheckoutCard(ctx context.Context, w http.ResponseWriter, c upgradeCaller, claims upgradeClaims, sessionID string) (state.APIConsumer, bool) {
	key, _, problem := s.Host.StripeKey(ctx, c.app.AccountID)
	if problem != nil {
		s.failed(ctx, c.app, "", "the Stripe connection cannot be used: "+problem.Detail)
		unavailable(w)
		return state.APIConsumer{}, false
	}
	stripe := s.stripe()
	session, err := stripe.GetCheckoutSession(ctx, key, sessionID)
	if err != nil {
		s.stripeFailed(ctx, w, c.app, "", err)
		return state.APIConsumer{}, false
	}
	if !session.Complete() || session.Customer != claims.Customer {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConsumerSignupCheckoutIncomplete, "Card not saved yet",
			"finish the checkout to save a card, then complete the upgrade"))
		return state.APIConsumer{}, false
	}
	if err := stripe.SetDefaultPaymentMethod(ctx, key, claims.Customer, session.SetupIntent.PaymentMethod); err != nil {
		s.stripeFailed(ctx, w, c.app, "", err)
		return state.APIConsumer{}, false
	}
	upgrades, ok := s.Host.Store().(state.APIConsumerUpgradeStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("upgrades are unavailable"))
		return state.APIConsumer{}, false
	}
	consumer, err := upgrades.AttachAPIConsumerStripeCustomer(ctx, c.app.AccountID, c.consumer.ID, claims.Customer)
	if errors.Is(err, state.ErrConflict) {
		billedElsewhere(w)
		return state.APIConsumer{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the card to the account; try again"))
		return state.APIConsumer{}, false
	}
	return consumer, true
}

// changePlan moves c's consumer to target from the next minute; already
// being on it is success, so a completed upgrade can be retried.
func (s *Service) changePlan(ctx context.Context, w http.ResponseWriter, c upgradeCaller, target string) {
	current := s.currentPlanID(ctx, c)
	if target == current {
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, api.ConsumerUpgradeResult{PlanID: target})
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
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Plan change pending",
			"a plan change starts at the next minute already; try again in a minute"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not change the plan"))
		return
	}
	if outbox, ok := s.Host.Store().(state.AppWebhookEventOutboxStore); ok {
		_, _ = outbox.RelayAppWebhookEventOutboxSource(ctx, state.AppWebhookEventConsumerPlanChanged, assignment.ID)
	}
	s.Host.Audit(ctx, "api_consumer.plan_changed_by_customer", c.app.AccountID, map[string]any{"app_id": c.app.ID,
		"consumer_id": c.consumer.ID, "from_plan_id": current, "to_plan_id": target, "effective_from": from, "via": "upgrade_link"})
	w.Header().Set("Cache-Control", "no-store")
	effective := assignment.EffectiveFrom
	httpkit.WriteJSON(w, http.StatusOK, api.ConsumerUpgradeResult{PlanID: assignment.PlanID, EffectiveFrom: &effective})
}
