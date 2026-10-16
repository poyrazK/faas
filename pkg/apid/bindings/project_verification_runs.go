// adr:1356 — review every member before persisting or admitting graph work.
package bindings

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"sort"
	"time"
)

type ProjectVerificationRunBackend struct {
	Store                  state.ProjectBindingVerificationRunStore
	Catalog                ProjectReleaseCatalog
	Project                state.Project
	AccountID, Environment string
	Member                 func(context.Context, string, string) (VerificationRunBackend, error)
}

func graphVerificationApps(r api.ProjectBindingVerificationRun) []string {
	names := make([]string, 0, len(r.Members))
	for name := range r.Members {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func (b ProjectVerificationRunBackend) Read(ctx context.Context, id string) (api.ProjectBindingVerificationRun, error) {
	r, err := b.Store.GetProjectBindingVerificationRun(ctx, b.AccountID, b.Project.ID, b.Environment, id)
	if err != nil {
		return r, err
	}
	for _, name := range graphVerificationApps(r) {
		member, err := b.Member(ctx, name, r.Members[name].Policy.AppID)
		if err != nil {
			return r, err
		}
		child, err := member.Read(ctx, r.Members[name].ID)
		if err != nil {
			return r, err
		}
		r.Members[name] = child
	}
	return r, nil
}
func (b ProjectVerificationRunBackend) Create(ctx context.Context, in api.CreateProjectBindingVerificationRunRequest) (api.ProjectBindingVerificationRun, error) {
	if in.MaxConcurrency < 0 || in.MaxConcurrency > api.BindingVerificationRunnerMaxConcurrency {
		return api.ProjectBindingVerificationRun{}, state.ErrInvalidArgument
	}
	in.MaxConcurrency = api.BindingVerificationConcurrency(in.MaxConcurrency)
	id, err := uuid.Parse(in.ID)
	if err != nil || id == uuid.Nil || id.String() != in.ID || in.TTLSeconds < 1 || in.TTLSeconds > api.BindingVerificationRunMaxTTLSeconds || in.Manifest.Project != b.Project.Slug || in.Manifest.Environment != b.Environment || len(in.ReviewDigests) != len(in.Manifest.Deployments) {
		return api.ProjectBindingVerificationRun{}, state.ErrInvalidArgument
	}
	digest := api.ProjectBindingVerificationReviewDigest(in)
	if in.ReviewDigest != "" && in.ReviewDigest != digest {
		return api.ProjectBindingVerificationRun{}, state.ErrConflict
	}
	if old, err := b.Read(ctx, in.ID); err == nil {
		if old.MaxConcurrency != in.MaxConcurrency || old.ReviewDigest != digest {
			return old, state.ErrConflict
		}
		return old, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return old, err
	}
	apps, err := b.Catalog.AppsForProject(ctx, b.AccountID, b.Project.ID)
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	if _, problem := ValidateCheckedProjectRelease(in.Manifest, b.Project.Slug, b.Environment, apps); problem != nil {
		return api.ProjectBindingVerificationRun{}, problem
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	report := api.ProjectBindingVerificationRun{MaxConcurrency: in.MaxConcurrency, ID: in.ID, Project: b.Project.Slug, Environment: b.Environment, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Duration(in.TTLSeconds) * time.Second), ReviewDigest: digest, Manifest: in.Manifest, ObjectStorageWrite: in.ObjectStorageWrite, QueuePublish: in.QueuePublish, Members: map[string]api.BindingVerificationRun{}}
	prepared := state.GraphVerificationCreate{Report: report, Members: map[string]state.VerificationRunCreate{}}
	// All catalogs, exact recipients and effect consent are reviewed before the
	// one transaction creates the graph and every child receipt.
	for _, app := range apps {
		member, err := b.Member(ctx, app.Slug, app.ID)
		if err != nil {
			return report, err
		}
		inventory, _, err := member.inventory(ctx, in.Manifest.Deployments[app.Slug], in.Manifest.ServiceTargets[app.Slug])
		if err != nil {
			return report, err
		}
		child, err := member.Prepare(ctx, api.CreateBindingVerificationRunRequest{MaxConcurrency: in.MaxConcurrency, ID: uuid.NewString(), DeploymentID: inventory.VerificationDeploymentID, ServiceTargets: in.Manifest.ServiceTargets[app.Slug], ReviewDigest: in.ReviewDigests[app.Slug], TTLSeconds: in.TTLSeconds, ObjectStorageWrite: in.ObjectStorageWrite, QueuePublish: in.QueuePublish}, now, report.ExpiresAt)
		if err != nil {
			return report, err
		}
		if child.Report.Policy.Revision != in.Manifest.ExpectedPolicyRevisions[app.Slug] {
			return report, state.ErrConflict
		}
		child.Report.GraphRunID = report.ID
		prepared.Members[app.Slug] = child
		prepared.Report.Members[app.Slug] = child.Report
	}
	return b.Store.CreateProjectBindingVerificationRun(ctx, b.AccountID, b.Project.ID, prepared)
}
func (b ProjectVerificationRunBackend) Resume(ctx context.Context, id string) (api.ProjectBindingVerificationRun, error) {
	r, err := b.Read(ctx, id)
	if err != nil {
		return r, err
	}
	return b.reviewGraph(ctx, r)
}
func (b ProjectVerificationRunBackend) reviewGraph(ctx context.Context, r api.ProjectBindingVerificationRun) (api.ProjectBindingVerificationRun, error) {
	captures := map[string][]state.VerificationRunAdmission{}
	for _, name := range graphVerificationApps(r) {
		member, err := b.Member(ctx, name, r.Members[name].Policy.AppID)
		if err != nil {
			return r, err
		}
		child := r.Members[name]
		if child.GraphRunID != r.ID || !child.ExpiresAt.Equal(r.ExpiresAt) {
			return r, state.ErrConflict
		}
		captures[name], err = member.review(ctx, child)
		if err != nil {
			return r, err
		}
	}
	err := b.Store.CheckProjectBindingVerificationRun(ctx, b.AccountID, b.Project.ID, b.Environment, r.ID, captures)
	return r, err
}

func (b ProjectVerificationRunBackend) Status(ctx context.Context, id string) (api.ProjectBindingVerificationRun, error) {
	r, err := b.Read(ctx, id)
	if err != nil || r.Status == "cancelled" || r.Status == "expired" {
		return r, err
	}
	validated, err := b.reviewGraph(ctx, r)
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrBindingReleasePolicyRevision) {
		r.Status, r.Reason = "blocked", "graph_or_member_review_changed"
		return r, nil
	}
	if err != nil {
		return r, err
	}
	return validated, nil
}
func (b ProjectVerificationRunBackend) Complete(ctx context.Context, id string) (api.ProjectBindingVerificationRun, error) {
	r, err := b.Resume(ctx, id)
	if err != nil {
		return r, err
	}
	pending := &api.BindingVerificationQualification{CheckedAt: time.Now().UTC(), Conditions: []api.BindingVerificationQualificationCondition{}}
	for _, name := range graphVerificationApps(r) {
		member, err := b.Member(ctx, name, r.Members[name].Policy.AppID)
		if err != nil {
			return r, err
		}
		child, err := member.Complete(ctx, r.Members[name].ID)
		if err != nil {
			return r, err
		}
		if child.Status == "pending" && child.Qualification != nil {
			if len(pending.Conditions)+len(child.Qualification.Conditions) > api.BindingVerificationQualificationMaxConditions {
				return r, state.ErrConflict
			}
			pending.Conditions = append(pending.Conditions, child.Qualification.Conditions...)
		} else if child.Status != "passed" {
			return r, state.ErrConflict
		}
		r.Members[name] = child
	}
	if len(pending.Conditions) > 0 {
		if _, err = b.reviewGraph(ctx, r); err != nil {
			return r, err
		}
		if !r.ExpiresAt.After(time.Now()) {
			return r, state.ErrConflict
		}
		pending.CheckedAt = time.Now().UTC()
		r.Qualification = pending
		return r, nil
	}
	if err = b.Store.SetProjectBindingVerificationRunStatus(ctx, b.AccountID, b.Project.ID, b.Environment, id, "passed"); err != nil {
		return r, err
	}
	r, err = b.Status(ctx, id)
	if err == nil && r.Status != "passed" {
		err = state.ErrConflict
	}
	return r, err
}
func (b ProjectVerificationRunBackend) Cancel(ctx context.Context, id string) (api.ProjectBindingVerificationRun, error) {
	if err := b.Store.SetProjectBindingVerificationRunStatus(ctx, b.AccountID, b.Project.ID, b.Environment, id, "cancelled"); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	r, err := b.Read(ctx, id)
	if err != nil {
		return r, err
	}
	// The parent fence is committed before any cooperative cleanup. Retry all
	// members even if one cleanup fails, retaining the first failure for retry.
	var failure error
	for _, name := range graphVerificationApps(r) {
		member, err := b.Member(ctx, name, r.Members[name].Policy.AppID)
		if err == nil {
			var child api.BindingVerificationRun
			child, err = member.Cancel(ctx, r.Members[name].ID)
			if err == nil {
				r.Members[name] = child
			}
		}
		if failure == nil {
			failure = err
		}
	}
	return r, failure
}
