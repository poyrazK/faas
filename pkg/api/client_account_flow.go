package api

import (
	"context"
	"net/url"
)

// Account flow client calls (ADR 20261107103000).

// ListSessions lists the account's active browser sessions.
func (c *Client) ListSessions(ctx context.Context) (SessionListResponse, error) {
	var out SessionListResponse
	return out, c.do(ctx, "GET", "/v1/auth/sessions", nil, &out)
}

// RevokeSession signs out one browser session.
func (c *Client) RevokeSession(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/auth/sessions/"+url.PathEscape(id), nil, nil)
}

// RevokeAllSessions signs out every browser session. API keys are kept.
func (c *Client) RevokeAllSessions(ctx context.Context) (SessionsRevokeAllResponse, error) {
	var out SessionsRevokeAllResponse
	return out, c.do(ctx, "POST", "/v1/auth/sessions/revoke_all", nil, &out)
}

// ResendEmailVerification mails a new verification link when the
// account's address is unverified. The status is "sent" or
// "already_verified".
func (c *Client) ResendEmailVerification(ctx context.Context) (string, error) {
	var out EmailVerificationResendResponse
	err := c.do(ctx, "POST", "/v1/auth/verify-email/resend", nil, &out)
	return out.Status, err
}

// SetDisplayName sets (or with "" clears) the account display name.
func (c *Client) SetDisplayName(ctx context.Context, name string) error {
	return c.do(ctx, "PATCH", "/v1/account", AccountProfileUpdate{DisplayName: name}, nil)
}

// SignInMethods lists the account's password, MFA and linked providers.
func (c *Client) SignInMethods(ctx context.Context) (SignInMethodsResponse, error) {
	var out SignInMethodsResponse
	return out, c.do(ctx, "GET", "/v1/account/sign-in-methods", nil, &out)
}

// LeaveOrg ends the caller's membership of the org.
func (c *Client) LeaveOrg(ctx context.Context, slug string) error {
	return c.do(ctx, "POST", "/v1/orgs/"+url.PathEscape(slug)+"/leave", nil, nil)
}

// UnlinkSignInMethod removes a linked sign-in provider ("google" or
// "github"). The account's last sign-in method cannot be removed.
func (c *Client) UnlinkSignInMethod(ctx context.Context, provider string) error {
	return c.do(ctx, "DELETE", "/v1/account/sign-in-methods/"+url.PathEscape(provider), nil, nil)
}
