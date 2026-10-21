package deploystaging

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectSubmissionKey struct{}
type ProjectSubmission struct {
	AccountID string
	Receipt   api.ProjectSubmissionReceipt
	store     state.ProjectSubmissionStore
	stop      chan struct{}
	selected  bool
	complete  bool
}

func ContextWithProjectSubmission(ctx context.Context, accountID, attemptID string) context.Context {
	return context.WithValue(ctx, projectSubmissionKey{}, &ProjectSubmission{AccountID: accountID, Receipt: api.ProjectSubmissionReceipt{AttemptID: attemptID, Status: "processing"}})
}
func ProjectSubmissionFromContext(ctx context.Context) *ProjectSubmission {
	r, _ := ctx.Value(projectSubmissionKey{}).(*ProjectSubmission)
	return r
}
func (p *ProjectSubmission) Begin(ctx context.Context, store state.ProjectSubmissionStore, slug, environment, fingerprint string) error {
	p.Receipt.ProjectSlug, p.Receipt.Environment = slug, environment
	reserved, err := store.ReserveProjectSubmission(ctx, p.AccountID, p.Receipt.AttemptID, fingerprint, p.Receipt)
	if err != nil {
		return err
	}
	if !reserved {
		return state.ErrProjectSubmissionExists
	}
	p.store = store
	p.stop = make(chan struct{})
	go p.heartbeat(context.WithoutCancel(ctx))
	return nil
}
func (p *ProjectSubmission) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = p.store.TouchProjectSubmission(callCtx, p.AccountID, p.Receipt.AttemptID)
			cancel()
		}
	}
}
func (p *ProjectSubmission) Select(ctx context.Context, project state.Project, apps []state.App) error {
	p.Receipt.SelectionComplete = true
	p.Receipt.Apply.ProjectID = project.ID
	p.Receipt.Apply.SubmissionAttemptID = p.Receipt.AttemptID
	for _, app := range apps {
		p.Receipt.Apply.Apps = append(p.Receipt.Apply.Apps, api.ApplyResponseApp{Slug: app.Slug, ID: app.ID})
		p.Receipt.Apply.Builds = append(p.Receipt.Apply.Builds, api.AppliedBuild{Slug: app.Slug, AppID: app.ID})
	}
	if err := p.store.WriteProjectSubmission(ctx, p.AccountID, p.Receipt); err != nil {
		return err
	}
	p.selected = true
	return nil
}
func (p *ProjectSubmission) Complete(builds []api.AppliedBuild) {
	p.complete = true
	p.Receipt.Apply.Builds = nil
	for _, row := range builds {
		safe := api.AppliedBuild{Slug: row.Slug, AppID: row.AppID, DeploymentID: row.DeploymentID, BuildID: row.BuildID}
		if row.Error != "" {
			safe.Error = "workload admission or enqueue did not complete; inspect deployment status"
		}
		p.Receipt.Apply.Builds = append(p.Receipt.Apply.Builds, safe)
	}
}
func (p *ProjectSubmission) Finish(ctx context.Context) error {
	if p.store == nil {
		return nil
	}
	close(p.stop)
	p.Receipt.Status = "rejected"
	if p.selected {
		p.Receipt.Status = "interrupted"
	}
	if p.complete {
		p.Receipt.Status = "complete"
	}
	return p.store.WriteProjectSubmission(ctx, p.AccountID, p.Receipt)
}
