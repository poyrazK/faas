package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/uuid"
)

// DeployCellApp creates image deployment intent for an immutable regional
// target. Use credentials issued by this cell, never a global directory key.
func (c *Client) DeployCellApp(ctx context.Context, cellID, appID string, req CreateDeploymentRequest) (DeploymentResponse, error) {
	var out DeploymentResponse
	if err := validateCellDeploymentIDs(cellID, appID); err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "POST", "/v1/cells/"+url.PathEscape(cellID)+"/apps/"+url.PathEscape(appID)+"/deployments", req, &out, "")
}

// DeployCellAppMultipart uploads the same source form as DeployMultipartWithSourceRoot,
// while retaining the cell/app UUID tuple through upload and retries.
func (c *Client) DeployCellAppMultipart(ctx context.Context, cellID, appID string, source io.Reader, sourceName, runtime, handler string, dockerfile bool, sourceRoot string, ann DeployAnnotations) (DeploymentResponse, error) {
	if err := validateCellDeploymentIDs(cellID, appID); err != nil {
		return DeploymentResponse{}, err
	}
	return c.deployMultipartToPath(ctx, "/v1/cells/"+url.PathEscape(cellID)+"/apps/"+url.PathEscape(appID)+"/deployments", "", source, sourceName, runtime, handler, dockerfile, sourceRoot, ann, cellDeploymentHTTP(c.uploadHTTP()))
}

func validateCellDeploymentIDs(cellID, appID string) error {
	for _, id := range []string{cellID, appID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return fmt.Errorf("cell_id and app_id must be canonical nonzero UUIDs")
		}
	}
	return nil
}

func cellDeploymentHTTP(client *http.Client) *http.Client {
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}
