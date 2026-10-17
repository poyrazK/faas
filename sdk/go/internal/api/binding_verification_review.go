// adr:1357 — server-issued, side-effect-free verification reviews.
package api

import (
	"context"
	"net/http"
	"time"
)

type ReviewBindingVerificationRequest struct {
	DeploymentID   string            `json:"deployment_id,omitempty"`
	ServiceTargets map[string]string `json:"service_targets,omitempty"`
}
type BindingVerificationReview struct {
	App                        string                        `json:"app"`
	DeploymentID               string                        `json:"deployment_id"`
	Scope                      string                        `json:"scope"`
	ServiceTargets             map[string]string             `json:"service_targets"`
	ReviewedAt                 time.Time                     `json:"reviewed_at"`
	Ready                      bool                          `json:"ready"`
	ReviewDigest               string                        `json:"review_digest"`
	ObjectStorageWriteRequired bool                          `json:"object_storage_write_required"`
	QueuePublishRequired       bool                          `json:"queue_publish_required"`
	Inventory                  AppBindingInventory           `json:"inventory"`
	Policy                     BindingReleasePolicy          `json:"policy"`
	Plan                       []BindingVerificationPlanStep `json:"plan"`
	Blockers                   []BindingCheckFinding         `json:"blockers"`
	ObservedCheck              *BindingCheckReport           `json:"observed_check,omitempty"`
}
type ReviewProjectBindingVerificationRequest struct {
	Manifest           CheckedProjectReleaseManifest `json:"manifest"`
	ObjectStorageWrite bool                          `json:"object_storage_write"`
	QueuePublish       bool                          `json:"queue_publish"`
}
type ProjectBindingVerificationReview struct {
	Project                    string                               `json:"project"`
	Environment                string                               `json:"environment"`
	ReviewedAt                 time.Time                            `json:"reviewed_at"`
	Ready                      bool                                 `json:"ready"`
	ReviewDigest               string                               `json:"review_digest"`
	ReviewDigests              map[string]string                    `json:"review_digests"`
	Manifest                   CheckedProjectReleaseManifest        `json:"manifest"`
	ObjectStorageWrite         bool                                 `json:"object_storage_write"`
	QueuePublish               bool                                 `json:"queue_publish"`
	ObjectStorageWriteRequired bool                                 `json:"object_storage_write_required"`
	QueuePublishRequired       bool                                 `json:"queue_publish_required"`
	Members                    map[string]BindingVerificationReview `json:"members"`
	Blockers                   []BindingCheckFinding                `json:"blockers"`
}

func (c *Client) ReviewBindingVerification(ctx context.Context, app string, in ReviewBindingVerificationRequest) (BindingVerificationReview, error) {
	var out BindingVerificationReview
	err := c.do(ctx, http.MethodPost, bindingRunPath(app, "")+"/review", in, &out)
	return out, err
}
func (c *Client) ReviewProjectBindingVerification(ctx context.Context, project, environment string, in ReviewProjectBindingVerificationRequest) (ProjectBindingVerificationReview, error) {
	var out ProjectBindingVerificationReview
	err := c.do(ctx, http.MethodPost, projectBindingRunPath(project, environment, "")+"/review", in, &out)
	return out, err
}
