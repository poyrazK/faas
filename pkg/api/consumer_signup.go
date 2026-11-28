package api

import (
	"context"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// Self-service sign-up for an app's API customers (ADR-20261021143000).

const (
	// CodeConsumerSignupRateLimited reports too many sign-up requests from
	// one client address.
	CodeConsumerSignupRateLimited = "consumer_signup_rate_limited"
	// CodeConsumerSignupDailyLimit reports that the app took its sign-ups
	// for the UTC day.
	CodeConsumerSignupDailyLimit = "consumer_signup_daily_limit"
	// CodeConsumerSignupCheckoutIncomplete reports a sign-up whose card is
	// not saved yet.
	CodeConsumerSignupCheckoutIncomplete = "consumer_signup_checkout_incomplete"
	// CodeConsumerSignupExpired reports a sign-up past its 24 hours.
	CodeConsumerSignupExpired = "consumer_signup_expired"
	// CodeConsumerSignupUnavailable reports that the app's billing setup
	// cannot take sign-ups right now; the owner gets an audit event.
	CodeConsumerSignupUnavailable = "consumer_signup_unavailable"
	// CodeConsumerEnrollmentUnavailable reports that the gateway could not
	// turn a verified OAuth caller into the app's consumer
	// (ADR-20261124094745), so the call is refused rather than served
	// unbilled.
	CodeConsumerEnrollmentUnavailable = "consumer_enrollment_unavailable"
	// CodeMCPGatewayPolicyMissing refuses turning OAuth consumers on while
	// the gateway does not check the MCP server's OAuth tokens: callers
	// would be served but never become customers (ADR-20261124094745).
	CodeMCPGatewayPolicyMissing = "mcp_gateway_policy_missing"
	// CodeConsumerLinkKeyRejected reports a key that is not a live key of
	// one of the app's consumers (ADR-20261124094745).
	CodeConsumerLinkKeyRejected = "consumer_link_key_rejected"
	// CodeConsumerLinkConflict reports an OAuth identity already linked to
	// another key's consumer; the app owner unlinks it first.
	CodeConsumerLinkConflict = "consumer_link_conflict"
)

// LinkConsumerRequest is the body of a signed-in MCP caller's
// POST /.well-known/gregale-consumer-link<endpoint path>: the consumer key
// it already holds for the app (ADR-20261124094745).
type LinkConsumerRequest struct {
	Key string `json:"key"`
}

// LinkConsumerResponse names the consumer the caller now bills to.
type LinkConsumerResponse struct {
	ConsumerID string `json:"consumer_id"`
}

// ConsumerIdentity is one OAuth sign-in of an app's MCP server and the
// consumer it bills to (ADR-20261124094745).
type ConsumerIdentity struct {
	ID         string    `json:"id"`
	ConsumerID string    `json:"consumer_id"`
	Issuer     string    `json:"issuer"`
	Subject    string    `json:"subject"`
	Email      string    `json:"email,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ConsumerIdentityListResponse lists an app's OAuth identities, newest
// first.
type ConsumerIdentityListResponse struct {
	Identities []ConsumerIdentity `json:"identities"`
}

// PutConsumerSignupRequest turns sign-up on for an app. Omitted fields
// take their defaults: enabled, Stripe, the app's default plan, keys with
// the write and billing scopes, no trial and ConsumerSignupDailyLimitDefault
// sign-ups a day.
type PutConsumerSignupRequest struct {
	Enabled     *bool    `json:"enabled,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	PlanIDs     []string `json:"plan_ids,omitempty"`
	KeyScopes   []string `json:"key_scopes,omitempty"`
	DisplayName string   `json:"display_name"`
	TermsURL    string   `json:"terms_url,omitempty"`
	ReturnURL   string   `json:"return_url,omitempty"`
	TrialDays   int      `json:"trial_days,omitempty"`
	DailyLimit  int      `json:"daily_limit,omitempty"`
	// CustomerPlanChanges lets customers switch between the offered plans,
	// and CustomerClosing lets them close their account, with their own key
	// (ADR-20261021183000).
	CustomerPlanChanges bool `json:"customer_plan_changes,omitempty"`
	CustomerClosing     bool `json:"customer_closing,omitempty"`
	// FreePlanID is an offered plan that charges nothing: joining it needs
	// no card. OAuthConsumers enrolls callers signed in through the app's
	// MCP oauth-proxy issuer as consumers on it (ADR-20261124094745).
	FreePlanID     string `json:"free_plan_id,omitempty"`
	OAuthConsumers bool   `json:"oauth_consumers,omitempty"`
	// AccountTools adds the account_usage and account_plans tools to the
	// app's MCP server for OAuth consumers; it needs OAuthConsumers
	// (ADR-20261125194510).
	AccountTools bool `json:"account_tools,omitempty"`
}

// ConsumerSignupSettingsResponse is an app's sign-up settings. PageURL is
// the hosted sign-up page to link customers to.
type ConsumerSignupSettingsResponse struct {
	App         string   `json:"app"`
	Enabled     bool     `json:"enabled"`
	Provider    string   `json:"provider"`
	PlanIDs     []string `json:"plan_ids"`
	KeyScopes   []string `json:"key_scopes"`
	DisplayName string   `json:"display_name"`
	TermsURL    string   `json:"terms_url,omitempty"`
	ReturnURL   string   `json:"return_url,omitempty"`
	TrialDays   int      `json:"trial_days,omitempty"`
	DailyLimit  int      `json:"daily_limit"`
	PageURL     string   `json:"page_url"`
	// CustomerPlanChanges and CustomerClosing are what customers may do
	// with their own key (ADR-20261021183000).
	CustomerPlanChanges bool      `json:"customer_plan_changes"`
	CustomerClosing     bool      `json:"customer_closing"`
	FreePlanID          string    `json:"free_plan_id,omitempty"`
	OAuthConsumers      bool      `json:"oauth_consumers"`
	AccountTools        bool      `json:"account_tools"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ConsumerSignupResponse is one sign-up as the app owner sees it.
type ConsumerSignupResponse struct {
	ID                 string     `json:"id"`
	Email              string     `json:"email"`
	Name               string     `json:"name"`
	PlanID             string     `json:"plan_id,omitempty"`
	Status             string     `json:"status"`
	Livemode           bool       `json:"livemode"`
	ProviderCustomerID string     `json:"provider_customer_id,omitempty"`
	ConsumerID         string     `json:"consumer_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          time.Time  `json:"expires_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

// ConsumerSignupListResponse lists an app's latest sign-ups, newest first.
type ConsumerSignupListResponse struct {
	Signups []ConsumerSignupResponse `json:"signups"`
}

// ConsumerSignupPrice is one unit's current price on a plan.
type ConsumerSignupPrice struct {
	Unit                      string `json:"unit"`
	Currency                  string `json:"currency"`
	PriceMillicentsPerUnit    int64  `json:"price_millicents_per_unit"`
	IncludedUnitsPerMonth     int64  `json:"included_units_per_month,omitempty"`
	Tiered                    bool   `json:"tiered,omitempty"`
	BaseFeeMillicentsPerMonth int64  `json:"base_fee_millicents_per_month,omitempty"`
	MinimumMillicentsPerMonth int64  `json:"minimum_millicents_per_month,omitempty"`
}

// ConsumerSignupPlan is a plan a customer can sign up for. An empty ID is
// the app's default plan.
type ConsumerSignupPlan struct {
	ID                   string                `json:"id,omitempty"`
	Name                 string                `json:"name"`
	MaxRequestsPerMinute int64                 `json:"max_requests_per_minute,omitempty"`
	MaxUnitsPerMonth     int64                 `json:"max_units_per_month,omitempty"`
	Prices               []ConsumerSignupPrice `json:"prices"`
	// Free marks the plan joining which needs no card (ADR-20261124094745).
	Free bool `json:"free,omitempty"`
}

// ConsumerSignupFormResponse is what the sign-up page shows.
type ConsumerSignupFormResponse struct {
	App         string               `json:"app"`
	DisplayName string               `json:"display_name"`
	TermsURL    string               `json:"terms_url,omitempty"`
	Livemode    bool                 `json:"livemode"`
	TrialDays   int                  `json:"trial_days,omitempty"`
	Plans       []ConsumerSignupPlan `json:"plans"`
}

// CreateConsumerSignupRequest starts a sign-up. PlanID may be empty when
// one plan is offered.
type CreateConsumerSignupRequest struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	PlanID string `json:"plan_id,omitempty"`
}

// Validate trims and checks the customer's email and name.
func (r *CreateConsumerSignupRequest) Validate() error {
	r.Email, r.Name, r.PlanID = strings.TrimSpace(r.Email), strings.TrimSpace(r.Name), strings.TrimSpace(r.PlanID)
	if addr, err := mail.ParseAddress(r.Email); err != nil || addr.Address != r.Email || len(r.Email) > 254 {
		return errors.New("email must be a plain email address of at most 254 characters")
	}
	if r.Name == "" || len([]rune(r.Name)) > 128 {
		return errors.New("name must be 1 to 128 characters")
	}
	return nil
}

// CreateConsumerSignupResponse sends the customer to CheckoutURL. The
// claim token completes the sign-up and is shown only here.
type CreateConsumerSignupResponse struct {
	ID string `json:"id"`
	// CheckoutURL is empty for the free plan: complete the sign-up at once
	// (ADR-20261124094745).
	CheckoutURL string    `json:"checkout_url"`
	ClaimToken  string    `json:"claim_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// CompleteConsumerSignupRequest completes a sign-up once the card is saved.
type CompleteConsumerSignupRequest struct {
	ClaimToken string `json:"claim_token"`
}

// CompleteConsumerSignupResponse is the customer's consumer and its new
// key, shown only here. Completing again issues a new key and revokes this
// one.
type CompleteConsumerSignupResponse struct {
	ConsumerID  string     `json:"consumer_id"`
	Key         string     `json:"key"`
	KeyPrefix   string     `json:"key_prefix"`
	Scopes      []string   `json:"scopes"`
	PlanID      string     `json:"plan_id,omitempty"`
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
	ReturnURL   string     `json:"return_url,omitempty"`
}

// ValidSignupURL reports whether u is an absolute https URL short enough to
// store.
func ValidSignupURL(u string) bool {
	parsed, err := url.Parse(u)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && len(u) <= 2048
}

func consumerSignupPath(app string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/consumer-signup"
}

// PutConsumerSignup turns sign-up on for an app or changes its settings.
func (c *Client) PutConsumerSignup(ctx context.Context, app string, req PutConsumerSignupRequest) (ConsumerSignupSettingsResponse, error) {
	var out ConsumerSignupSettingsResponse
	return out, c.do(ctx, "PUT", consumerSignupPath(app), req, &out)
}

// GetConsumerSignup reads an app's sign-up settings.
func (c *Client) GetConsumerSignup(ctx context.Context, app string) (ConsumerSignupSettingsResponse, error) {
	var out ConsumerSignupSettingsResponse
	return out, c.do(ctx, "GET", consumerSignupPath(app), nil, &out)
}

// DeleteConsumerSignup turns sign-up off and removes its settings.
func (c *Client) DeleteConsumerSignup(ctx context.Context, app string) error {
	return c.do(ctx, "DELETE", consumerSignupPath(app), nil, nil)
}

// ListConsumerSignups lists an app's latest sign-ups.
func (c *Client) ListConsumerSignups(ctx context.Context, app string) (ConsumerSignupListResponse, error) {
	var out ConsumerSignupListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/consumer-signups", nil, &out)
}

// ListConsumerIdentities lists the OAuth sign-ins of an app's MCP server
// and the consumers they bill to (ADR-20261124094745).
func (c *Client) ListConsumerIdentities(ctx context.Context, app string) (ConsumerIdentityListResponse, error) {
	var out ConsumerIdentityListResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/consumer-identities", nil, &out)
}

// DeleteConsumerIdentity unlinks an OAuth sign-in from its consumer.
func (c *Client) DeleteConsumerIdentity(ctx context.Context, app, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+url.PathEscape(app)+"/consumer-identities/"+url.PathEscape(id), nil, nil)
}

// GetConsumerUpgrade reads the plans a customer's upgrade link offers. It
// needs no credential: token is the link's token.
func (c *Client) GetConsumerUpgrade(ctx context.Context, app, token string) (ConsumerUpgradeResponse, error) {
	var out ConsumerUpgradeResponse
	return out, c.do(ctx, "GET", "/v1/consumer-signup/"+url.PathEscape(app)+"/upgrade/plans?token="+url.QueryEscape(token), nil, &out)
}

// StartConsumerUpgrade moves the link's customer to req.PlanID, or returns
// the Stripe Checkout that saves their card first.
func (c *Client) StartConsumerUpgrade(ctx context.Context, app string, req StartConsumerUpgradeRequest) (ConsumerUpgradeResult, error) {
	var out ConsumerUpgradeResult
	return out, c.do(ctx, "POST", "/v1/consumer-signup/"+url.PathEscape(app)+"/upgrade", req, &out)
}

// CompleteConsumerUpgrade finishes an upgrade after Checkout saved the card.
func (c *Client) CompleteConsumerUpgrade(ctx context.Context, app string, req CompleteConsumerUpgradeRequest) (ConsumerUpgradeResult, error) {
	var out ConsumerUpgradeResult
	return out, c.do(ctx, "POST", "/v1/consumer-signup/"+url.PathEscape(app)+"/upgrade/complete", req, &out)
}

// GetConsumerSignupForm reads an app's public sign-up form. It needs no
// credential.
func (c *Client) GetConsumerSignupForm(ctx context.Context, app string) (ConsumerSignupFormResponse, error) {
	var out ConsumerSignupFormResponse
	return out, c.do(ctx, "GET", "/v1/consumer-signup/"+url.PathEscape(app), nil, &out)
}

// CreateConsumerSignup starts a customer's sign-up. It needs no credential.
func (c *Client) CreateConsumerSignup(ctx context.Context, app string, req CreateConsumerSignupRequest) (CreateConsumerSignupResponse, error) {
	var out CreateConsumerSignupResponse
	return out, c.do(ctx, "POST", "/v1/consumer-signup/"+url.PathEscape(app), req, &out)
}

// CompleteConsumerSignup completes a sign-up with its claim token and
// returns the customer's key. It needs no credential.
func (c *Client) CompleteConsumerSignup(ctx context.Context, app, signupID string, req CompleteConsumerSignupRequest) (CompleteConsumerSignupResponse, error) {
	var out CompleteConsumerSignupResponse
	return out, c.do(ctx, "POST", "/v1/consumer-signup/"+url.PathEscape(app)+"/signups/"+url.PathEscape(signupID)+"/complete", req, &out)
}

// App webhook events about customers acting on their own account
// (ADR-20261021183000).
const (
	WebhookEventConsumerSignedUp    = "consumer.signed_up"
	WebhookEventConsumerPlanChanged = "consumer.plan_changed"
	WebhookEventConsumerClosed      = "consumer.closed"
)

// APIConsumerSignedUpWebhookPayload is delivered with consumer.signed_up
// once a customer's sign-up created their consumer. signup_id is stable
// across retries.
type APIConsumerSignedUpWebhookPayload struct {
	SignupID    string     `json:"signup_id"`
	AppID       string     `json:"app_id"`
	ConsumerID  string     `json:"consumer_id"`
	ExternalRef string     `json:"external_ref"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	PlanID      string     `json:"plan_id,omitempty"`
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
	Livemode    bool       `json:"livemode"`
	SignedUpAt  time.Time  `json:"signed_up_at"`
}

// APIConsumerPlanChangedWebhookPayload is delivered with
// consumer.plan_changed when a customer switched plans with their own key.
// assignment_id is stable across retries; an empty plan ID is the app's
// default plan.
type APIConsumerPlanChangedWebhookPayload struct {
	AssignmentID  string    `json:"assignment_id"`
	AppID         string    `json:"app_id"`
	ConsumerID    string    `json:"consumer_id"`
	ExternalRef   string    `json:"external_ref"`
	FromPlanID    string    `json:"from_plan_id,omitempty"`
	ToPlanID      string    `json:"to_plan_id,omitempty"`
	EffectiveFrom time.Time `json:"effective_from"`
	ChangedAt     time.Time `json:"changed_at"`
}

// ConsumerUpgradeResponse is what the upgrade page shows a customer who
// opened an upgrade link (ADR-20261124094745).
type ConsumerUpgradeResponse struct {
	App           string               `json:"app"`
	DisplayName   string               `json:"display_name"`
	CurrentPlanID string               `json:"current_plan_id,omitempty"`
	Livemode      bool                 `json:"livemode"`
	Plans         []ConsumerSignupPlan `json:"plans"`
	// HasCard reports a saved card: moving to a paid plan needs no checkout.
	HasCard bool `json:"has_card"`
}

// StartConsumerUpgradeRequest moves the upgrade link's customer to PlanID.
type StartConsumerUpgradeRequest struct {
	Token  string `json:"token"`
	PlanID string `json:"plan_id"`
}

// ConsumerUpgradeResult is a started or finished upgrade. A paid plan for a
// customer without a card returns CheckoutURL: Stripe Checkout returns to
// the upgrade page, which completes the upgrade with CompleteToken.
// Otherwise the plan changes from EffectiveFrom.
type ConsumerUpgradeResult struct {
	PlanID        string     `json:"plan_id"`
	EffectiveFrom *time.Time `json:"effective_from,omitempty"`
	CheckoutURL   string     `json:"checkout_url,omitempty"`
	CompleteToken string     `json:"complete_token,omitempty"`
}

// CompleteConsumerUpgradeRequest finishes an upgrade after Checkout saved
// the card.
type CompleteConsumerUpgradeRequest struct {
	Token             string `json:"token"`
	CompleteToken     string `json:"complete_token"`
	CheckoutSessionID string `json:"checkout_session_id"`
}

// APIConsumerClosedWebhookPayload is delivered with consumer.closed when a
// customer closed their account with their own key.
type APIConsumerClosedWebhookPayload struct {
	AppID       string    `json:"app_id"`
	ConsumerID  string    `json:"consumer_id"`
	ExternalRef string    `json:"external_ref"`
	ClosedAt    time.Time `json:"closed_at"`
}
