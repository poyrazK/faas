package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Claimable deploys (ADR-20261124080000). An agent deploys with no account:
// apid creates a locked-down Free account for it and returns a deploy key and
// a claim link. A human claims the account by proving an email address; an
// unclaimed account past its expiry is scheduled for deletion.

// UnclaimedEmailDomain is the reserved domain of an unclaimed account's
// placeholder address. ".invalid" never resolves (RFC 2606), and the mailer
// drops every message to it before any transport sees it.
const UnclaimedEmailDomain = "unclaimed.invalid"

// IsUnclaimedPlaceholderEmail reports whether addr is an unclaimed account's
// placeholder address. Mail to it is dropped.
func IsUnclaimedPlaceholderEmail(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	return strings.HasSuffix(addr, "@"+UnclaimedEmailDomain)
}

const (
	// CodeClaimRequired refuses an unclaimed account a route it may only
	// use after a human claims it (custom domains, databases, keys, ...).
	CodeClaimRequired = "claim_required"
	// CodeAnonymousDeploysUnavailable refuses a new anonymous session: the
	// platform cap is reached, the caller's network used its share, or the
	// caller's network resembles an abuse-held account.
	CodeAnonymousDeploysUnavailable = "anonymous_deploys_unavailable"
	// CodeClaimExpired answers a claim-link request after the account's
	// claim window ended (410).
	CodeClaimExpired = "claim_expired"
)

// ErrClaimExpired is the refusal for a claim link after the window ended.
func ErrClaimExpired() *Problem {
	return NewProblem(http.StatusGone, CodeClaimExpired, "Claim window ended",
		"this deploy without an account expired; run gregale login and deploy again").
		WithDocs(docsBase + "/claimable-deploys")
}

// ErrClaimRequired is the refusal for a route an unclaimed account cannot
// use yet.
func ErrClaimRequired() *Problem {
	return NewProblem(http.StatusForbidden, CodeClaimRequired,
		"Claim this account first",
		"this app was deployed without an account. Open the claim link (gregale claim prints it) to sign up and unlock custom domains, databases, more apps and API keys.").
		WithDocs(docsBase + "/claimable-deploys")
}

// ErrAnonymousDeploysUnavailable refuses a new anonymous session.
func ErrAnonymousDeploysUnavailable(retryAfterSeconds int) *Problem {
	p := NewProblem(http.StatusTooManyRequests, CodeAnonymousDeploysUnavailable,
		"Anonymous deploys unavailable",
		"deploying without an account is not available from here right now. Sign up with gregale login and deploy again.").
		WithDocs(docsBase + "/claimable-deploys")
	if retryAfterSeconds > 0 {
		p = p.WithHeader("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	return p
}

// AnonymousSessionRequest is the body of POST /v1/anonymous/sessions.
type AnonymousSessionRequest struct {
	// Client names the tool creating the session, such as gregale-cli/1.2.3.
	Client string `json:"client,omitempty"`
	// Accept must be true: the caller accepts the Terms of Service.
	AcceptTerms bool `json:"accept_terms"`
}

// AnonymousSessionResponse answers POST /v1/anonymous/sessions.
type AnonymousSessionResponse struct {
	AccountID string `json:"account_id"`
	// APIKey is the plaintext deploy key, shown once.
	APIKey string `json:"api_key"`
	// ClaimURL is the link a human opens to claim the account.
	ClaimURL  string    `json:"claim_url"`
	ExpiresAt time.Time `json:"expires_at"`
	// Limits describes what the account may do until it is claimed.
	Limits UnclaimedLimits `json:"limits"`
}

// UnclaimedLimits is what an unclaimed account may use.
type UnclaimedLimits struct {
	Plan    string   `json:"plan"`
	MaxApps int      `json:"max_apps"`
	MaxRAM  int      `json:"max_ram_mb"`
	Blocked []string `json:"blocked"`
}

// ClaimLinkResponse answers POST /v1/account/claim-link.
type ClaimLinkResponse struct {
	ClaimURL  string    `json:"claim_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// UnclaimedBlockedFeatures lists, for people, what an unclaimed account
// cannot use until it is claimed.
var UnclaimedBlockedFeatures = []string{
	"custom domains", "managed databases and caches", "object storage buckets",
	"new API keys and deploy tokens", "organizations and invitations",
	"billing and plan changes", "cron jobs", "outbound email",
}

// CreateAnonymousSession creates an anonymous deploy session: a locked-down
// account, its deploy key and a claim link (POST /v1/anonymous/sessions).
// The client needs no token.
func (c *Client) CreateAnonymousSession(ctx context.Context, req AnonymousSessionRequest) (AnonymousSessionResponse, error) {
	var out AnonymousSessionResponse
	return out, c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/anonymous/sessions", req, &out)
}

// CreateClaimLink returns a fresh claim link for the unclaimed account the
// client's key belongs to; the previous link stops working
// (POST /v1/account/claim-link).
func (c *Client) CreateClaimLink(ctx context.Context) (ClaimLinkResponse, error) {
	var out ClaimLinkResponse
	return out, c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/account/claim-link", nil, &out)
}
