package api

import "context"

type SourceReviewRequest struct {
	Repo        string `json:"repo"`
	Ref         string `json:"ref"`
	Environment string `json:"environment,omitempty"`
}

// SourceReviewResponse pins provider identity and target configuration without
// downloading source, publishing a build, or changing repository declarations.
type SourceReviewResponse struct {
	AppID          string           `json:"app_id"`
	Repo           string           `json:"repo"`
	CommitSHA      string           `json:"commit_sha"`
	InstallationID int64            `json:"installation_id"`
	RepositoryID   int64            `json:"repository_id"`
	SourceRoot     string           `json:"source_root"`
	ReleaseReview  AppReleaseReview `json:"release_review"`
}

func (c *Client) ReviewSourceRef(ctx context.Context, app string, input SourceReviewRequest) (SourceReviewResponse, error) {
	var out SourceReviewResponse
	err := c.do(ctx, "POST", "/v1/apps/"+app+"/source-review", input, &out)
	return out, err
}
