// adr:1356 — durable qualification of a frozen release graph.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

type CreateProjectBindingVerificationRunRequest struct {
	MaxConcurrency     int                           `json:"max_concurrency,omitempty"`
	ID                 string                        `json:"id"`
	ReviewDigest       string                        `json:"review_digest,omitempty"`
	Manifest           CheckedProjectReleaseManifest `json:"manifest"`
	ReviewDigests      map[string]string             `json:"review_digests"`
	TTLSeconds         int                           `json:"ttl_seconds"`
	ObjectStorageWrite bool                          `json:"object_storage_write"`
	QueuePublish       bool                          `json:"queue_publish"`
}
type ProjectBindingVerificationRun struct {
	Qualification      *BindingVerificationQualification `json:"qualification,omitempty"`
	MaxConcurrency     int                               `json:"max_concurrency,omitempty"`
	ID                 string                            `json:"id"`
	Project            string                            `json:"project"`
	Environment        string                            `json:"environment"`
	Status             string                            `json:"status"`
	Reason             string                            `json:"reason,omitempty"`
	CreatedAt          time.Time                         `json:"created_at"`
	ExpiresAt          time.Time                         `json:"expires_at"`
	ReviewDigest       string                            `json:"review_digest"`
	Manifest           CheckedProjectReleaseManifest     `json:"manifest"`
	ObjectStorageWrite bool                              `json:"object_storage_write"`
	QueuePublish       bool                              `json:"queue_publish"`
	Members            map[string]BindingVerificationRun `json:"members"`
}

func ProjectBindingVerificationReviewDigest(in CreateProjectBindingVerificationRunRequest) string {
	raw, _ := json.Marshal(struct {
		Manifest     CheckedProjectReleaseManifest
		Digests      map[string]string
		Write, Queue bool
	}{in.Manifest, in.ReviewDigests, in.ObjectStorageWrite, in.QueuePublish})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func projectBindingRunPath(project, environment, id string) string {
	p := projectReleaseSetsPath(project, environment) + "/verification-runs"
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p
}
func (c *Client) CreateProjectBindingVerificationRun(ctx context.Context, project, environment string, in CreateProjectBindingVerificationRunRequest) (ProjectBindingVerificationRun, error) {
	var out ProjectBindingVerificationRun
	err := c.do(ctx, http.MethodPost, projectBindingRunPath(project, environment, ""), in, &out)
	return out, err
}
func (c *Client) GetProjectBindingVerificationRun(ctx context.Context, project, environment, id string) (ProjectBindingVerificationRun, error) {
	var out ProjectBindingVerificationRun
	err := c.do(ctx, http.MethodGet, projectBindingRunPath(project, environment, id), nil, &out)
	return out, err
}
func (c *Client) ResumeProjectBindingVerificationRun(ctx context.Context, project, environment, id string) (ProjectBindingVerificationRun, error) {
	var out ProjectBindingVerificationRun
	err := c.do(ctx, http.MethodPost, projectBindingRunPath(project, environment, id)+"/resume", nil, &out)
	return out, err
}
func (c *Client) CancelProjectBindingVerificationRun(ctx context.Context, project, environment, id string) (ProjectBindingVerificationRun, error) {
	var out ProjectBindingVerificationRun
	err := c.do(ctx, http.MethodDelete, projectBindingRunPath(project, environment, id), nil, &out)
	return out, err
}
func (c *Client) CompleteProjectBindingVerificationRun(ctx context.Context, project, environment, id string) (ProjectBindingVerificationRun, error) {
	var out ProjectBindingVerificationRun
	err := c.do(ctx, http.MethodPost, projectBindingRunPath(project, environment, id)+"/complete", nil, &out)
	return out, err
}
