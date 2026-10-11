package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// ProjectEnvironmentCloneOperationResponse is a durable, non-secret receipt.
// Worker lease tokens and private captured configuration are never exposed.
type ProjectEnvironmentCloneOperationResponse struct {
	OperationID        string                                    `json:"operation_id"`
	ProjectSlug        string                                    `json:"project_slug"`
	SourceEnvironment  string                                    `json:"source_environment"`
	TargetEnvironment  string                                    `json:"target_environment"`
	SourceRevisionHash string                                    `json:"source_revision_hash"`
	SourceReleaseSetID string                                    `json:"source_release_set_id,omitempty"`
	TargetReleaseSetID string                                    `json:"target_release_set_id,omitempty"`
	Status             string                                    `json:"status"`
	Revision           int64                                     `json:"revision"`
	Resources          []ProjectEnvironmentCloneResourceResponse `json:"resources"`
	ErrorCode          string                                    `json:"error_code,omitempty"`
	CreatedAt          string                                    `json:"created_at"`
	UpdatedAt          string                                    `json:"updated_at"`
}

type ProjectEnvironmentCloneResourceResponse struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	SourceID      string `json:"source_id,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	TargetID      string `json:"target_id,omitempty"`
	CapturePoint  string `json:"capture_point,omitempty"`
	Status        string `json:"status"`
}

// CreateFullProjectEnvironmentClone uses a distinct route so an older server
// cannot silently treat a full-copy request as a partial environment clone.
func (c *Client) CreateFullProjectEnvironmentClone(ctx context.Context, projectSlug string, req CreateProjectEnvironmentRequest) (ProjectEnvironmentResponse, error) {
	var out ProjectEnvironmentResponse
	if !ValidProjectEnvironmentSlug(req.Slug) || !ValidProjectEnvironmentSlug(req.FromEnvironment) || req.Slug == req.FromEnvironment || req.ShareResources {
		return out, errors.New("full clone requires different source and target environments and isolated resources")
	}
	req.Full = true
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environment-clones"
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return out, err
	}
	if out.CloneOperation == nil || out.CloneOperation.OperationID == "" || out.CloneOperation.ProjectSlug != projectSlug ||
		out.CloneOperation.SourceEnvironment != req.FromEnvironment || out.CloneOperation.TargetEnvironment != req.Slug {
		return out, errors.New("full clone response did not contain the requested durable operation")
	}
	return out, nil
}

func (c *Client) GetProjectEnvironmentCloneOperation(ctx context.Context, projectSlug, operationID string) (ProjectEnvironmentCloneOperationResponse, error) {
	var out ProjectEnvironmentCloneOperationResponse
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environment-clones/" + url.PathEscape(operationID)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
