package api

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type CreateMCPReadinessMaintenanceRequest struct {
	Scope    string    `json:"scope"`
	StartsAt time.Time `json:"starts_at,omitempty"`
	EndsAt   time.Time `json:"ends_at"`
	Reason   string    `json:"reason"`
}
type CancelMCPReadinessMaintenanceRequest struct {
	Reason string `json:"reason"`
}
type MCPReadinessMaintenanceWindow struct {
	ID           string     `json:"id"`
	Scope        string     `json:"scope"`
	StartsAt     time.Time  `json:"starts_at"`
	EndsAt       time.Time  `json:"ends_at"`
	Reason       string     `json:"reason"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	CancelledAt  *time.Time `json:"cancelled_at,omitempty"`
	CancelledBy  string     `json:"cancelled_by,omitempty"`
	CancelReason string     `json:"cancel_reason,omitempty"`
}
type MCPReadinessMaintenancePage struct {
	Windows    []MCPReadinessMaintenanceWindow `json:"windows"`
	NextBefore string                          `json:"next_before,omitempty"`
}

func ValidMCPMaintenanceReason(reason string) bool {
	return utf8.ValidString(reason) && !strings.ContainsRune(reason, 0) && len(strings.TrimSpace(reason)) > 0 && len(reason) <= 500
}
func ValidateMCPReadinessMaintenance(r CreateMCPReadinessMaintenanceRequest, now time.Time) error {
	start := r.StartsAt
	if start.IsZero() {
		start = now
	}
	// Each failure names the field, so the CLI can say what to change
	// instead of one sentence covering every rule (ADR-20261110090000).
	switch {
	case ValidateScope(MCPConnectionScope(r.Scope)) != nil:
		return errors.New("environment is not a valid name")
	case !ValidMCPMaintenanceReason(r.Reason):
		return errors.New("reason must be 1–500 bytes of text")
	case r.EndsAt.IsZero():
		return errors.New("an end is required: give --end or --duration")
	case start.Before(now.Add(-time.Minute)):
		return errors.New("start is in the past")
	case !r.EndsAt.After(now) || !r.EndsAt.After(start):
		return errors.New("end must be after the start and in the future")
	case r.EndsAt.Sub(start) > 30*24*time.Hour:
		return errors.New("a window lasts at most 30 days (720h)")
	}
	return nil
}
func (c *Client) ListMCPReadinessMaintenance(ctx context.Context, app, scope, before string, limit int) (MCPReadinessMaintenancePage, error) {
	q := url.Values{"scope": {MCPConnectionScope(scope)}}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		q.Set("before", before)
	}
	var out MCPReadinessMaintenancePage
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-maintenance?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) CreateMCPReadinessMaintenance(ctx context.Context, app string, r CreateMCPReadinessMaintenanceRequest) (MCPReadinessMaintenanceWindow, error) {
	var out MCPReadinessMaintenanceWindow
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-maintenance", r, &out)
	return out, err
}
func (c *Client) CancelMCPReadinessMaintenance(ctx context.Context, app, id string, r CancelMCPReadinessMaintenanceRequest) (MCPReadinessMaintenanceWindow, error) {
	var out MCPReadinessMaintenanceWindow
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-maintenance/"+url.PathEscape(id)+"/cancel", r, &out)
	return out, err
}
