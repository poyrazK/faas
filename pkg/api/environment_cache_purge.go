package api

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type CreateEnvironmentCachePurgeRequest struct {
	AppID         string `json:"app_id"`
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`
	RequestID     string `json:"request_id"`
	Path          string `json:"path"`
}
type EnvironmentCachePurgeReceipt struct {
	AppID         string    `json:"app_id"`
	ProjectID     string    `json:"project_id"`
	EnvironmentID string    `json:"environment_id"`
	Scope         string    `json:"scope"`
	RequestID     string    `json:"request_id"`
	Path          string    `json:"path"`
	Generation    int64     `json:"generation"`
	RequestedAt   time.Time `json:"requested_at"`
}
type EnvironmentCachePurgeStatus struct {
	EnvironmentCachePurgeReceipt
	Status               string    `json:"status"`
	CheckedAt            time.Time `json:"checked_at"`
	ServingGateways      int64     `json:"serving_gateways"`
	AcknowledgedGateways int64     `json:"acknowledged_gateways"`
	PendingGateways      int64     `json:"pending_gateways"`
	StaleGateways        int64     `json:"stale_gateways"`
}

func ValidateEnvironmentCachePurgePath(value string) error {
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) || len(value) > CachePurgeGlobMaxBytes || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#%\\*[]{}") {
		return errors.New("purge path must be an exact decoded URL path, or empty for the whole environment")
	}
	for _, c := range value {
		if c < 32 || c == 127 {
			return errors.New("purge path contains control characters")
		}
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return errors.New("purge path must not contain dot segments")
		}
	}
	return nil
}
func ValidateEnvironmentCachePurgeRequest(req CreateEnvironmentCachePurgeRequest) error {
	for _, id := range []string{req.AppID, req.ProjectID, req.EnvironmentID, req.RequestID} {
		if _, err := uuid.Parse(id); err != nil {
			return errors.New("immutable UUID identities and request_id are required")
		}
	}
	return ValidateEnvironmentCachePurgePath(req.Path)
}
func (c *Client) CreateEnvironmentCachePurge(ctx context.Context, slug, environment string, req CreateEnvironmentCachePurgeRequest) (EnvironmentCachePurgeReceipt, error) {
	var out EnvironmentCachePurgeReceipt
	err := c.doWithIdempotencyKey(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/cache/purges?environment="+url.QueryEscape(environment), req, &out, req.RequestID)
	return out, err
}
func (c *Client) GetEnvironmentCachePurge(ctx context.Context, slug, environment, requestID string) (EnvironmentCachePurgeStatus, error) {
	var out EnvironmentCachePurgeStatus
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/cache/purges/"+url.PathEscape(requestID)+"?environment="+url.QueryEscape(environment), nil, &out)
	return out, err
}
