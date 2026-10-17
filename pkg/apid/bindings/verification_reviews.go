package bindings

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"maps"
	"time"
)

// BuildVerificationReview uses public facts only. It never captures private
// commands, creates a receipt, consumes capacity, or admits work.
func BuildVerificationReview(i api.AppBindingInventory, p api.BindingReleasePolicy, now time.Time) api.BindingVerificationReview {
	plan, blockers, observed, err := bindingcheck.VerificationPlan(i, p, now)
	out := api.BindingVerificationReview{App: i.App, DeploymentID: i.VerificationDeploymentID, Scope: p.Scope, ServiceTargets: maps.Clone(i.ServiceTargets), ReviewedAt: now, Inventory: i, Policy: p, Plan: plan, Blockers: blockers, Ready: err == nil}
	if out.ServiceTargets == nil {
		out.ServiceTargets = map[string]string{}
	}
	if out.Blockers == nil {
		out.Blockers = []api.BindingCheckFinding{}
	}
	if out.Plan == nil {
		out.Plan = []api.BindingVerificationPlanStep{}
	}
	if observed != nil {
		converted := api.BindingCheckReport(*observed)
		out.ObservedCheck = &converted
	}
	if err != nil && len(out.Blockers) == 0 {
		out.Blockers = append(out.Blockers, api.BindingCheckFinding{Code: "verification_plan_unavailable", Message: "An enforced policy and a complete valid verification plan are required."})
	}
	if len(out.Plan) > api.BindingVerificationRunMaxSteps {
		out.Ready = false
		out.Blockers = append(out.Blockers, api.BindingCheckFinding{Code: "verification_run_step_limit", Message: "The plan exceeds the durable verification step limit."})
	}
	for _, step := range out.Plan {
		out.QueuePublishRequired = out.QueuePublishRequired || step.Action == "queue-canary"
		out.ObjectStorageWriteRequired = out.ObjectStorageWriteRequired || step.Action == "probe" && step.Type == api.BindingTypeObjectStorage && step.ProbeMode != "read"
	}
	if out.Ready {
		out.ReviewDigest = bindingcheck.VerificationReviewDigest(i, p)
	}
	return out
}
func (b VerificationRunBackend) Review(ctx context.Context, in api.ReviewBindingVerificationRequest) (api.BindingVerificationReview, error) {
	if len(in.ServiceTargets) > api.ProjectReleaseSetMaxMembers {
		return api.BindingVerificationReview{}, state.ErrInvalidArgument
	}
	if in.DeploymentID != "" {
		if id, err := uuid.Parse(in.DeploymentID); err != nil || id == uuid.Nil {
			return api.BindingVerificationReview{}, state.ErrInvalidArgument
		}
	}
	i, p, err := b.inventory(ctx, in.DeploymentID, in.ServiceTargets)
	if err != nil {
		return api.BindingVerificationReview{}, err
	}
	if in.DeploymentID != "" {
		id, _ := uuid.Parse(in.DeploymentID)
		selected, err := uuid.Parse(i.VerificationDeploymentID)
		if err != nil || id != selected {
			return api.BindingVerificationReview{}, state.ErrConflict
		}
	}
	if !validVerificationRunInventory(i, p, b.App, b.AppID, i.VerificationDeploymentID) {
		return api.BindingVerificationReview{}, state.ErrConflict
	}
	review := BuildVerificationReview(i, p, time.Now().UTC())
	if b.TaskProbesEnabled != nil && !*b.TaskProbesEnabled {
		for _, step := range review.Plan {
			if step.Action == "probe" || step.Action == "wait" || step.Action == "reuse" && step.Type != api.BindingTypeQueue {
				review.Ready = false
				review.ReviewDigest = ""
				review.Blockers = append(review.Blockers, api.BindingCheckFinding{Type: step.Type, Name: step.Name, Binding: step.Binding, Scope: step.Scope, Code: "task_probes_unavailable", Message: "The task API required to freeze this probe is unavailable."})
				break
			}
		}
	}
	return review, nil
}
func (b ProjectVerificationRunBackend) Review(ctx context.Context, in api.ReviewProjectBindingVerificationRequest) (api.ProjectBindingVerificationReview, error) {
	out := api.ProjectBindingVerificationReview{Project: b.Project.Slug, Environment: b.Environment, ReviewedAt: time.Now().UTC(), Manifest: in.Manifest, ObjectStorageWrite: in.ObjectStorageWrite, QueuePublish: in.QueuePublish, Members: map[string]api.BindingVerificationReview{}, ReviewDigests: map[string]string{}, Blockers: []api.BindingCheckFinding{}, Ready: true}
	apps, err := b.Catalog.AppsForProject(ctx, b.AccountID, b.Project.ID)
	if err != nil {
		return out, err
	}
	if _, problem := ValidateCheckedProjectRelease(in.Manifest, b.Project.Slug, b.Environment, apps); problem != nil {
		return out, problem
	}
	if err = b.confirmReviewManifest(ctx, in.Manifest); err != nil {
		return out, err
	}
	for _, app := range apps {
		member, err := b.Member(ctx, app.Slug, app.ID)
		if err != nil {
			return out, err
		}
		review, err := member.Review(ctx, api.ReviewBindingVerificationRequest{DeploymentID: in.Manifest.Deployments[app.Slug], ServiceTargets: in.Manifest.ServiceTargets[app.Slug]})
		if err != nil {
			return out, err
		}
		if review.Policy.Revision != in.Manifest.ExpectedPolicyRevisions[app.Slug] {
			return out, state.ErrConflict
		}
		out.Members[app.Slug] = review
		out.ReviewDigests[app.Slug] = review.ReviewDigest
		out.Ready = out.Ready && review.Ready
		out.ObjectStorageWriteRequired = out.ObjectStorageWriteRequired || review.ObjectStorageWriteRequired
		out.QueuePublishRequired = out.QueuePublishRequired || review.QueuePublishRequired
		out.Blockers = append(out.Blockers, review.Blockers...)
	}
	// Reconfirm membership/routing after the bounded member reads. Creation still
	// rechecks private facts and graph authority in its own transaction.
	if err = b.confirmReviewManifest(ctx, in.Manifest); err != nil {
		return out, err
	}
	if out.Ready {
		out.ReviewDigest = api.ProjectBindingVerificationReviewDigest(api.CreateProjectBindingVerificationRunRequest{Manifest: out.Manifest, ReviewDigests: out.ReviewDigests, ObjectStorageWrite: out.ObjectStorageWrite, QueuePublish: out.QueuePublish})
	}
	return out, nil
}
func (b ProjectVerificationRunBackend) confirmReviewManifest(ctx context.Context, m api.CheckedProjectReleaseManifest) error {
	var current api.CheckedProjectReleaseManifest
	var problem *api.Problem
	switch {
	case m.Deactivate:
		current, problem = PrepareProjectDeactivation(ctx, b.Catalog, b.AccountID, b.Project, b.Environment)
	case m.RollbackReleaseID != "":
		catalog, ok := b.Catalog.(ProjectRollbackCatalog)
		if !ok {
			return api.ErrCapacity("rollback review is unavailable")
		}
		current, problem = PrepareProjectRollback(ctx, catalog, b.AccountID, b.Project, b.Environment, api.PrepareProjectRollbackRequest{ReleaseID: m.RollbackReleaseID, TTLSeconds: m.TTLSeconds})
	default:
		current, problem = PrepareProjectRelease(ctx, b.Catalog, b.AccountID, b.Project, b.Environment, api.PublishProjectReleaseSetRequest{Deployments: m.Deployments, TTLSeconds: m.TTLSeconds})
	}
	if problem != nil {
		return problem
	}
	// JSON treats equivalent map ordering identically and preserves explicit
	// empty maps and routing CAS fields from the prepared public contract.
	a, _ := json.Marshal(m)
	c, _ := json.Marshal(current)
	if !bytes.Equal(a, c) {
		return state.ErrConflict
	}
	return nil
}
