package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// SupportTicketListOptions filters a page of customer-visible support cases.
type SupportTicketListOptions struct {
	Cursor string
	Status string
	Search string
}

type SupportNotificationListOptions struct {
	Before int64
	Unread bool
}

func supportNotificationsPath(base string, options SupportNotificationListOptions) string {
	query := url.Values{}
	if options.Before != 0 {
		query.Set("before", strconv.FormatInt(options.Before, 10))
	}
	if options.Unread {
		query.Set("unread", "true")
	}
	if len(query) == 0 {
		return base
	}
	return base + "?" + query.Encode()
}

// ListSupportNotifications calls GET /v1/support/notifications.
func (c *Client) ListSupportNotifications(ctx context.Context, options SupportNotificationListOptions) (SupportNotificationPage, error) {
	var out SupportNotificationPage
	err := c.do(ctx, http.MethodGet, supportNotificationsPath("/v1/support/notifications", options), nil, &out)
	return out, err
}

// ListOrgSupportNotifications calls GET /v1/orgs/{org}/support/notifications.
func (c *Client) ListOrgSupportNotifications(ctx context.Context, organization string, options SupportNotificationListOptions) (SupportNotificationPage, error) {
	var out SupportNotificationPage
	err := c.do(ctx, http.MethodGet, supportNotificationsPath("/v1/orgs/"+url.PathEscape(organization)+"/support/notifications", options), nil, &out)
	return out, err
}

// MarkSupportTicketRead calls POST /v1/support/tickets/{ticket}/read. Only updates
// through request.ThroughSequence are marked; a later notification stays unread.
func (c *Client) MarkSupportTicketRead(ctx context.Context, ticket string, request SupportReadRequest) (SupportReadResult, error) {
	var out SupportReadResult
	err := c.do(ctx, http.MethodPost, "/v1/support/tickets/"+url.PathEscape(ticket)+"/read", request, &out)
	return out, err
}

// MarkOrgSupportTicketRead calls POST /v1/orgs/{org}/support/tickets/{ticket}/read.
func (c *Client) MarkOrgSupportTicketRead(ctx context.Context, organization, ticket string, request SupportReadRequest) (SupportReadResult, error) {
	var out SupportReadResult
	err := c.do(ctx, http.MethodPost, orgSupportTicketsPath(organization)+"/"+url.PathEscape(ticket)+"/read", request, &out)
	return out, err
}

func supportListPath(base string, options SupportTicketListOptions) string {
	query := url.Values{}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.Search != "" {
		query.Set("search", options.Search)
	}
	if len(query) == 0 {
		return base
	}
	return base + "?" + query.Encode()
}

func supportMessagesPath(base, ticket string, after int64) string {
	path := base + "/" + url.PathEscape(ticket) + "/messages"
	if after != 0 {
		path += "?after=" + strconv.FormatInt(after, 10)
	}
	return path
}

func orgSupportTicketsPath(organization string) string {
	return "/v1/orgs/" + url.PathEscape(organization) + "/support/tickets"
}

// ListSupportTickets calls GET /v1/support/tickets.
func (c *Client) ListSupportTickets(ctx context.Context, options SupportTicketListOptions) (SupportTicketPage, error) {
	const base = "/v1/support/tickets"
	var out SupportTicketPage
	err := c.do(ctx, http.MethodGet, supportListPath(base, options), nil, &out)
	return out, err
}

// CreateSupportTicket calls POST /v1/support/tickets.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) CreateSupportTicket(ctx context.Context, request CreateSupportTicketRequest) (SupportSubmission, error) {
	const base = "/v1/support/tickets"
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base, request, &out)
	return out, err
}

// GetSupportTicket calls GET /v1/support/tickets/{ticket}.
func (c *Client) GetSupportTicket(ctx context.Context, ticket string) (SupportTicket, error) {
	const base = "/v1/support/tickets"
	var out SupportTicket
	err := c.do(ctx, http.MethodGet, base+"/"+url.PathEscape(ticket), nil, &out)
	return out, err
}

// ListSupportMessages calls GET /v1/support/tickets/{ticket}/messages.
func (c *Client) ListSupportMessages(ctx context.Context, ticket string, after int64) (SupportMessagePage, error) {
	const base = "/v1/support/tickets"
	var out SupportMessagePage
	err := c.do(ctx, http.MethodGet, supportMessagesPath(base, ticket, after), nil, &out)
	return out, err
}

// ReplySupportTicket calls POST /v1/support/tickets/{ticket}/messages.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) ReplySupportTicket(ctx context.Context, ticket string, request SupportMessageRequest) (SupportSubmission, error) {
	const base = "/v1/support/tickets"
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/messages", request, &out)
	return out, err
}

// CloseSupportTicket calls POST /v1/support/tickets/{ticket}/close.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) CloseSupportTicket(ctx context.Context, ticket string, request CloseSupportTicketRequest) (SupportSubmission, error) {
	const base = "/v1/support/tickets"
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/close", request, &out)
	return out, err
}

// ReopenSupportTicket calls POST /v1/support/tickets/{ticket}/reopen.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) ReopenSupportTicket(ctx context.Context, ticket string, request ReopenSupportTicketRequest) (SupportSubmission, error) {
	const base = "/v1/support/tickets"
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/reopen", request, &out)
	return out, err
}

// GetSupportSatisfaction calls GET /v1/support/tickets/{ticket}/satisfaction.
func (c *Client) GetSupportSatisfaction(ctx context.Context, ticket string) (SupportSatisfaction, error) {
	const base = "/v1/support/tickets"
	var out SupportSatisfaction
	err := c.do(ctx, http.MethodGet, base+"/"+url.PathEscape(ticket)+"/satisfaction", nil, &out)
	return out, err
}

// SetSupportSatisfaction calls POST /v1/support/tickets/{ticket}/satisfaction.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) SetSupportSatisfaction(ctx context.Context, ticket string, request SupportSatisfactionRequest) (SupportSatisfactionSubmission, error) {
	const base = "/v1/support/tickets"
	var out SupportSatisfactionSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/satisfaction", request, &out)
	return out, err
}

// ListOrgSupportTickets calls GET /v1/orgs/{org}/support/tickets.
func (c *Client) ListOrgSupportTickets(ctx context.Context, organization string, options SupportTicketListOptions) (SupportTicketPage, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportTicketPage
	err := c.do(ctx, http.MethodGet, supportListPath(base, options), nil, &out)
	return out, err
}

// CreateOrgSupportTicket calls POST /v1/orgs/{org}/support/tickets.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) CreateOrgSupportTicket(ctx context.Context, organization string, request CreateSupportTicketRequest) (SupportSubmission, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base, request, &out)
	return out, err
}

// GetOrgSupportTicket calls GET /v1/orgs/{org}/support/tickets/{ticket}.
func (c *Client) GetOrgSupportTicket(ctx context.Context, organization string, ticket string) (SupportTicket, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportTicket
	err := c.do(ctx, http.MethodGet, base+"/"+url.PathEscape(ticket), nil, &out)
	return out, err
}

// ListOrgSupportMessages calls GET /v1/orgs/{org}/support/tickets/{ticket}/messages.
func (c *Client) ListOrgSupportMessages(ctx context.Context, organization string, ticket string, after int64) (SupportMessagePage, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportMessagePage
	err := c.do(ctx, http.MethodGet, supportMessagesPath(base, ticket, after), nil, &out)
	return out, err
}

// ReplyOrgSupportTicket calls POST /v1/orgs/{org}/support/tickets/{ticket}/messages.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) ReplyOrgSupportTicket(ctx context.Context, organization string, ticket string, request SupportMessageRequest) (SupportSubmission, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/messages", request, &out)
	return out, err
}

// CloseOrgSupportTicket calls POST /v1/orgs/{org}/support/tickets/{ticket}/close.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) CloseOrgSupportTicket(ctx context.Context, organization string, ticket string, request CloseSupportTicketRequest) (SupportSubmission, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/close", request, &out)
	return out, err
}

// ReopenOrgSupportTicket calls POST /v1/orgs/{org}/support/tickets/{ticket}/reopen.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) ReopenOrgSupportTicket(ctx context.Context, organization string, ticket string, request ReopenSupportTicketRequest) (SupportSubmission, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/reopen", request, &out)
	return out, err
}

// GetOrgSupportSatisfaction calls GET /v1/orgs/{org}/support/tickets/{ticket}/satisfaction.
func (c *Client) GetOrgSupportSatisfaction(ctx context.Context, organization string, ticket string) (SupportSatisfaction, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportSatisfaction
	err := c.do(ctx, http.MethodGet, base+"/"+url.PathEscape(ticket)+"/satisfaction", nil, &out)
	return out, err
}

// SetOrgSupportSatisfaction calls POST /v1/orgs/{org}/support/tickets/{ticket}/satisfaction.
// Use ContextWithIdempotencyKey with the same UUID when retrying an operation.
func (c *Client) SetOrgSupportSatisfaction(ctx context.Context, organization string, ticket string, request SupportSatisfactionRequest) (SupportSatisfactionSubmission, error) {
	base := orgSupportTicketsPath(organization)
	var out SupportSatisfactionSubmission
	err := c.do(ctx, http.MethodPost, base+"/"+url.PathEscape(ticket)+"/satisfaction", request, &out)
	return out, err
}
