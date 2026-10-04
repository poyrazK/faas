package api

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// Keys authorize this request only. Resuming pending regions requires their keys.
type PutGlobalImageReleaseRequest struct {
	Image           string            `json:"image"`
	RegionalAPIKeys map[string]string `json:"regional_api_keys,omitempty"`
}

type GlobalImageReleaseResponse struct {
	GlobalAppID       string                             `json:"global_app_id"`
	ReleaseID         string                             `json:"release_id"`
	Generation        int64                              `json:"generation"`
	DesiredGeneration int64                              `json:"desired_generation"`
	Image             string                             `json:"image"`
	Status            string                             `json:"status"`
	CreatedAt         time.Time                          `json:"created_at"`
	Targets           []GlobalImageReleaseTargetResponse `json:"targets"`
}

type GlobalImageReleaseTargetResponse struct {
	CellID            string    `json:"cell_id"`
	Region            string    `json:"region"`
	CellGeneration    int64     `json:"cell_generation"`
	RegionalAccountID string    `json:"regional_account_id"`
	RegionalOrgID     string    `json:"regional_org_id,omitempty"`
	RegionalAppID     string    `json:"regional_app_id"`
	RegionalAppSlug   string    `json:"regional_app_slug"`
	Outcome           string    `json:"outcome"`
	Diagnostic        string    `json:"diagnostic"`
	DeploymentID      *string   `json:"deployment_id"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (c *Client) PutGlobalImageRelease(ctx context.Context, slug, releaseID string, req PutGlobalImageReleaseRequest) (GlobalImageReleaseResponse, error) {
	var out GlobalImageReleaseResponse
	path, err := globalImageReleasePath(slug, releaseID)
	if err != nil {
		return out, err
	}
	client := *c.http
	if client.Timeout < GlobalImageReleaseRequestTimeout+GlobalImageReleaseRegionalTimeout {
		client.Timeout = GlobalImageReleaseRequestTimeout + GlobalImageReleaseRegionalTimeout
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(&client), "PUT", path, req, &out, releaseID)
}

func (c *Client) GetGlobalImageRelease(ctx context.Context, slug, releaseID string) (GlobalImageReleaseResponse, error) {
	var out GlobalImageReleaseResponse
	path, err := globalImageReleasePath(slug, releaseID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "GET", path, nil, &out, "")
}

func globalImageReleasePath(slug, releaseID string) (string, error) {
	id, err := uuid.Parse(releaseID)
	if err != nil || id == uuid.Nil || id.String() != releaseID {
		return "", fmt.Errorf("release_id must be a canonical nonzero UUID")
	}
	return "/v1/global-apps/" + url.PathEscape(slug) + "/image-releases/" + url.PathEscape(releaseID), nil
}
