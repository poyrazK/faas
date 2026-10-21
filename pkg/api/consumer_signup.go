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
)

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
}

// ConsumerSignupSettingsResponse is an app's sign-up settings. PageURL is
// the hosted sign-up page to link customers to.
type ConsumerSignupSettingsResponse struct {
	App         string    `json:"app"`
	Enabled     bool      `json:"enabled"`
	Provider    string    `json:"provider"`
	PlanIDs     []string  `json:"plan_ids"`
	KeyScopes   []string  `json:"key_scopes"`
	DisplayName string    `json:"display_name"`
	TermsURL    string    `json:"terms_url,omitempty"`
	ReturnURL   string    `json:"return_url,omitempty"`
	TrialDays   int       `json:"trial_days,omitempty"`
	DailyLimit  int       `json:"daily_limit"`
	PageURL     string    `json:"page_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	ID          string    `json:"id"`
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
