// adr:1355 — durable receipts preserve reviewed verification plans.
package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type BindingVerificationPlanStep struct {
	TargetDeploymentID string     `json:"target_deployment_id,omitempty"`
	Type               string     `json:"type"`
	Name               string     `json:"name"`
	Binding            string     `json:"binding"`
	Scope              string     `json:"scope"`
	QueueEnvironment   *string    `json:"queue_environment,omitempty"`
	Action             string     `json:"action"`
	Reason             string     `json:"reason,omitempty"`
	RequiredEvidence   string     `json:"required_evidence,omitempty"`
	CheckedAt          *time.Time `json:"checked_at,omitempty"`
	Prefix             string     `json:"prefix,omitempty"`
	RequiredMode       string     `json:"required_mode,omitempty"`
	ProbeMode          string     `json:"probe_mode,omitempty"`
	Operations         []string   `json:"operations,omitempty"`
}

type CreateBindingVerificationRunRequest struct {
	MaxConcurrency     int               `json:"max_concurrency,omitempty"`
	ID                 string            `json:"id"`
	ServiceTargets     map[string]string `json:"service_targets,omitempty"`
	DeploymentID       string            `json:"deployment_id"`
	ReviewDigest       string            `json:"review_digest"`
	TTLSeconds         int               `json:"ttl_seconds"`
	ObjectStorageWrite bool              `json:"object_storage_write"`
	QueuePublish       bool              `json:"queue_publish"`
}
type BindingVerificationRunStep struct {
	Plan         BindingVerificationPlanStep `json:"plan"`
	ResourceID   string                      `json:"resource_id,omitempty"`
	ResourceKind string                      `json:"resource_kind,omitempty"`
	Owned        bool                        `json:"owned"`
	Status       string                      `json:"status"`
}
type BindingVerificationRun struct {
	Qualification      *BindingVerificationQualification `json:"qualification,omitempty"`
	MaxConcurrency     int                               `json:"max_concurrency,omitempty"`
	ID                 string                            `json:"id"`
	GraphRunID         string                            `json:"graph_run_id,omitempty"`
	App                string                            `json:"app"`
	DeploymentID       string                            `json:"deployment_id"`
	Scope              string                            `json:"scope"`
	Status             string                            `json:"status"`
	Reason             string                            `json:"reason,omitempty"`
	CreatedAt          time.Time                         `json:"created_at"`
	ExpiresAt          time.Time                         `json:"expires_at"`
	ObjectStorageWrite bool                              `json:"object_storage_write"`
	QueuePublish       bool                              `json:"queue_publish"`
	ReviewDigest       string                            `json:"review_digest"`
	Policy             BindingReleasePolicy              `json:"policy"`
	Inventory          AppBindingInventory               `json:"inventory"`
	Steps              []BindingVerificationRunStep      `json:"steps"`
}

func bindingRunPath(app, id string) string {
	p := "/v1/apps/" + url.PathEscape(app) + "/bindings/verification-runs"
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p
}
func (c *Client) CreateBindingVerificationRun(ctx context.Context, app string, in CreateBindingVerificationRunRequest) (BindingVerificationRun, error) {
	var out BindingVerificationRun
	err := c.do(ctx, http.MethodPost, bindingRunPath(app, ""), in, &out)
	return out, err
}
func (c *Client) GetBindingVerificationRun(ctx context.Context, app, id string) (BindingVerificationRun, error) {
	var out BindingVerificationRun
	err := c.do(ctx, http.MethodGet, bindingRunPath(app, id), nil, &out)
	return out, err
}
func (c *Client) ResumeBindingVerificationRun(ctx context.Context, app, id string) (BindingVerificationRun, error) {
	var out BindingVerificationRun
	err := c.do(ctx, http.MethodPost, bindingRunPath(app, id)+"/resume", nil, &out)
	return out, err
}
func (c *Client) CancelBindingVerificationRun(ctx context.Context, app, id string) (BindingVerificationRun, error) {
	var out BindingVerificationRun
	err := c.do(ctx, http.MethodDelete, bindingRunPath(app, id), nil, &out)
	return out, err
}
func (c *Client) CompleteBindingVerificationRun(ctx context.Context, app, id string) (BindingVerificationRun, error) {
	var out BindingVerificationRun
	err := c.do(ctx, http.MethodPost, bindingRunPath(app, id)+"/complete", nil, &out)
	return out, err
}
func (c *Client) AdmitBindingVerificationRunStep(ctx context.Context, app, id string, step int) (BindingVerificationRunStep, error) {
	var out BindingVerificationRunStep
	err := c.do(ctx, http.MethodPost, bindingRunPath(app, id)+"/steps/"+strconv.Itoa(step)+"/admit", nil, &out)
	return out, err
}

// BindingVerificationConcurrency resolves the default for omitted or legacy budgets.
func BindingVerificationConcurrency(value int) int {
	if value == 0 {
		return BindingVerificationRunnerDefaultConcurrency
	}
	return value
}
