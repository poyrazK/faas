package faas

import (
	"context"
	"github.com/poyrazK/faas/sdk/go/internal/api"
	"github.com/poyrazK/faas/sdk/go/internal/bindingrun"
)

type BindingVerificationRunnerOptions = bindingrun.Options
type BindingVerificationProgress = bindingrun.Progress
type BindingVerificationRecoveryError = bindingrun.RecoveryError

// RunBindingVerification resumes an existing receipt and qualifies it on the
// server. Context expiry, progress errors and network failures preserve work.
func (c *Client) RunBindingVerification(ctx context.Context, app, id string, options BindingVerificationRunnerOptions) (BindingVerificationRun, error) {
	return bindingrun.RunApp(ctx, c, app, id, options)
}

// RunProjectBindingVerification shares one concurrency budget across members.
func (c *Client) RunProjectBindingVerification(ctx context.Context, project, environment, id string, options BindingVerificationRunnerOptions) (ProjectBindingVerificationRun, error) {
	return bindingrun.RunProject(ctx, c, project, environment, id, options)
}

// BindingVerificationCreationOptions requires a UUID persisted before creation.
type BindingVerificationCreationOptions = bindingrun.CreationOptions
type BindingVerificationCreationError = bindingrun.CreationError

func (c *Client) CreateBindingVerificationFromReview(ctx context.Context, app string, review BindingVerificationReview, options BindingVerificationCreationOptions) (BindingVerificationRun, error) {
	return bindingrun.CreateApp(ctx, c, app, review, options)
}
func (c *Client) ReconcileBindingVerificationCreation(ctx context.Context, app string, review BindingVerificationReview, options BindingVerificationCreationOptions) (BindingVerificationRun, error) {
	return bindingrun.ReconcileApp(ctx, c, app, review, options)
}
func (c *Client) CreateProjectBindingVerificationFromReview(ctx context.Context, project, environment string, review ProjectBindingVerificationReview, options BindingVerificationCreationOptions) (ProjectBindingVerificationRun, error) {
	return bindingrun.CreateProject(ctx, c, project, environment, review, options)
}
func (c *Client) ReconcileProjectBindingVerificationCreation(ctx context.Context, project, environment string, review ProjectBindingVerificationReview, options BindingVerificationCreationOptions) (ProjectBindingVerificationRun, error) {
	return bindingrun.ReconcileProject(ctx, c, project, environment, review, options)
}

type BindingVerificationRunSummary = api.BindingVerificationRunSummary
type BindingVerificationRunList = api.BindingVerificationRunList
type BindingVerificationRunListOptions = api.BindingVerificationRunListOptions

func (c *Client) ListBindingVerificationRuns(ctx context.Context, app string, o BindingVerificationRunListOptions) (BindingVerificationRunList, error) {
	return c.Client.ListBindingVerificationRuns(ctx, app, o)
}
func (c *Client) ListProjectBindingVerificationRuns(ctx context.Context, project, environment string, o BindingVerificationRunListOptions) (BindingVerificationRunList, error) {
	return c.Client.ListProjectBindingVerificationRuns(ctx, project, environment, o)
}

type BindingVerificationQualification = api.BindingVerificationQualification
type BindingVerificationQualificationCondition = api.BindingVerificationQualificationCondition
