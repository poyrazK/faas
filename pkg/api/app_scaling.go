// adr: 20261104104521
package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// AppScalingSettings contains the settings changed by a reviewed scaling operation.
type AppScalingSettings struct {
	RAMMB          int `json:"ram_mb"`
	CPUMillicores  int `json:"cpu_millicores"`
	MaxConcurrency int `json:"max_concurrency"`
	MinInstances   int `json:"min_instances"`
}

type AppScalingSnapshot struct {
	EnvironmentID string             `json:"environment_id,omitempty"`
	AppID         string             `json:"app_id"`
	Scope         string             `json:"scope"`
	Revision      int64              `json:"revision"`
	Settings      AppScalingSettings `json:"settings"`
}

type ApplyAppScalingRequest struct {
	ExpectedAppID         string `json:"expected_app_id"`
	ExpectedEnvironmentID string `json:"expected_environment_id,omitempty"`
	RequestID             string `json:"request_id"`
	ExpectedRevision      *int64 `json:"expected_revision"`
	RAMMB                 *int   `json:"ram_mb,omitempty"`
	CPUMillicores         *int   `json:"cpu_millicores,omitempty"`
	MaxConcurrency        *int   `json:"max_concurrency,omitempty"`
	MinInstances          *int   `json:"min_instances,omitempty"`
}

func (r *ApplyAppScalingRequest) Validate() *Problem {
	id, err := uuid.Parse(r.RequestID)
	if err != nil || id == uuid.Nil || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 {
		return ErrValidation("request_id must be a UUID and expected_revision must be non-negative")
	}
	appID, err := uuid.Parse(r.ExpectedAppID)
	if err != nil || appID == uuid.Nil {
		return ErrValidation("expected_app_id must be the reviewed app UUID")
	}
	r.ExpectedAppID = appID.String()
	if r.ExpectedEnvironmentID != "" {
		envID, err := uuid.Parse(r.ExpectedEnvironmentID)
		if err != nil || envID == uuid.Nil {
			return ErrValidation("expected_environment_id must be a UUID")
		}
		r.ExpectedEnvironmentID = envID.String()
	}
	r.RequestID = id.String()
	if r.RAMMB == nil && r.CPUMillicores == nil && r.MaxConcurrency == nil && r.MinInstances == nil {
		return ErrValidation("provide at least one scaling setting")
	}
	return nil
}

func (r ApplyAppScalingRequest) UpdateAppRequest() UpdateAppRequest {
	return UpdateAppRequest{RAMMB: r.RAMMB, CPUMillicores: r.CPUMillicores, MaxConcurrency: r.MaxConcurrency, MinInstances: r.MinInstances}
}

// AppScalingReceipt proves a configuration commit, not guest readiness.
type AppScalingReceipt struct {
	AppScalingSnapshot
	RequestID        string    `json:"request_id"`
	PreviousRevision int64     `json:"previous_revision"`
	CommittedAt      time.Time `json:"committed_at"`
}

func (c *Client) GetAppScaling(ctx context.Context, app, scope string) (AppScalingSnapshot, error) {
	var out AppScalingSnapshot
	err := c.do(ctx, http.MethodGet, appScalingPath(app, scope, ""), nil, &out)
	return out, err
}

func (c *Client) ApplyAppScaling(ctx context.Context, app, scope string, req ApplyAppScalingRequest) (AppScalingReceipt, error) {
	var out AppScalingReceipt
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, appScalingPath(app, scope, ""), req, &out)
	return out, err
}

func (c *Client) GetAppScalingReceipt(ctx context.Context, app, scope, requestID string) (AppScalingReceipt, error) {
	var out AppScalingReceipt
	err := c.do(ctx, http.MethodGet, appScalingPath(app, scope, "/requests/"+url.PathEscape(requestID)), nil, &out)
	return out, err
}

func appScalingPath(app, scope, suffix string) string {
	path := "/v1/apps/" + url.PathEscape(app) + "/scaling" + suffix
	if scope != "" && scope != "default" {
		path += "?environment=" + url.QueryEscape(scope)
	}
	return path
}
