package api

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
)

type PutCellImageReleaseRequest struct {
	Image    string                    `json:"image"`
	Ordering *CellImageReleaseOrdering `json:"ordering,omitempty"`
}

// CellImageReleaseOrdering fences delayed first acceptance of older releases.
type CellImageReleaseOrdering struct {
	GlobalAppID string `json:"global_app_id"`
	Generation  int64  `json:"generation"`
}

func (o CellImageReleaseOrdering) Valid() bool {
	id, err := uuid.Parse(o.GlobalAppID)
	return err == nil && id != uuid.Nil && id.String() == o.GlobalAppID && o.Generation > 0 && o.Generation <= GlobalImageReleaseGenerationMax
}

// CellImageReleaseResponse reports regional acceptance, not global readiness.
// DeploymentID is null after collection; that release can never be executed again.
type CellImageReleaseResponse struct {
	CellID            string                    `json:"cell_id"`
	Region            string                    `json:"region"`
	RegionalAccountID string                    `json:"regional_account_id"`
	RegionalAppID     string                    `json:"regional_app_id"`
	ReleaseID         string                    `json:"release_id"`
	Image             string                    `json:"image"`
	DeploymentID      *string                   `json:"deployment_id"`
	DeploymentStatus  string                    `json:"deployment_status"`
	CreatedAt         time.Time                 `json:"created_at"`
	Replayed          bool                      `json:"replayed"`
	Ordering          *CellImageReleaseOrdering `json:"ordering,omitempty"`
}

// PutCellImageRelease accepts one digest-pinned image per release UUID, forever.
// It requires a key issued by the target cell and refuses HTTP redirects.
func (c *Client) PutCellImageRelease(ctx context.Context, cellID, appID, releaseID string, req PutCellImageReleaseRequest) (CellImageReleaseResponse, error) {
	var out CellImageReleaseResponse
	path, err := cellImageReleasePath(cellID, appID, releaseID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "PUT", path, req, &out, releaseID)
}

func (c *Client) GetCellImageRelease(ctx context.Context, cellID, appID, releaseID string) (CellImageReleaseResponse, error) {
	var out CellImageReleaseResponse
	path, err := cellImageReleasePath(cellID, appID, releaseID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "GET", path, nil, &out, "")
}

func cellImageReleasePath(cellID, appID, releaseID string) (string, error) {
	if err := validateCellDeploymentIDs(cellID, appID); err != nil {
		return "", err
	}
	parsed, err := uuid.Parse(releaseID)
	if err != nil || parsed == uuid.Nil || parsed.String() != releaseID {
		return "", fmt.Errorf("release_id must be a canonical nonzero UUID")
	}
	return "/v1/cells/" + url.PathEscape(cellID) + "/apps/" + url.PathEscape(appID) + "/image-releases/" + url.PathEscape(releaseID), nil
}
