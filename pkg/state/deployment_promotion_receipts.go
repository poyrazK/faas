package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type DeploymentPromotionReceiptStore interface {
	DeploymentPromotionReceipt(context.Context, string, string) (api.DeploymentPromotionReceipt, error)
}

var ErrDeploymentPromotionAttemptAccepted = errors.New("promotion attempt already accepted; inspect its retained receipt")
var ErrDeploymentPromotionAttemptConflict = errors.New("promotion attempt binding conflicts with its original request")

type deploymentPromotionAttempt struct {
	ID      string
	Request api.UpdateDeploymentTrafficRequest
}
type deploymentPromotionKey struct{}

// ContextWithDeploymentPromotion is APID's validated, transient write metadata.
func ContextWithDeploymentPromotion(ctx context.Context, id string, request api.UpdateDeploymentTrafficRequest) context.Context {
	return context.WithValue(ctx, deploymentPromotionKey{}, deploymentPromotionAttempt{id, request})
}
func promotionReceiptFor(ctx context.Context, d Deployment, percent int, serving []string, reviews []*api.AppReleaseReview) (*api.DeploymentPromotionReceipt, error) {
	attempt, ok := ctx.Value(deploymentPromotionKey{}).(deploymentPromotionAttempt)
	if !ok {
		return nil, nil
	}
	if !api.ValidDeploymentAttemptID(attempt.ID) || percent != 100 || len(serving) != 1 || attempt.Request.ExpectedServingDeploymentID == nil {
		return nil, ErrInvalidArgument
	}
	request := api.UpdateDeploymentTrafficRequest{TrafficPercent: percent, ExpectedServingDeploymentID: &serving[0], ExpectedMCPAccessPolicy: attempt.Request.ExpectedMCPAccessPolicy}
	if len(reviews) > 0 {
		if len(reviews) != 1 || reviews[0] == nil {
			return nil, ErrInvalidArgument
		}
		request.ExpectedReview = reviews[0]
	}
	hash, err := api.DeploymentPromotionRequestHash(d.ID, request)
	if err != nil {
		return nil, err
	}
	original, err := api.DeploymentPromotionRequestHash(d.ID, attempt.Request)
	if err != nil || original != hash {
		return nil, ErrInvalidArgument
	}
	r := &api.DeploymentPromotionReceipt{AttemptID: attempt.ID, RequestHash: hash, AppID: d.AppID, DeploymentID: d.ID, ExpectedServingDeploymentID: serving[0], Scope: normalizedDeploymentScope(d.Scope), AcceptedAt: time.Now().UTC()}
	if r.Scope != api.DefaultEnvScope {
		if request.ExpectedReview == nil || request.ExpectedReview.AppID != d.AppID || request.ExpectedReview.Scope != r.Scope || request.ExpectedReview.EnvironmentID == "" || request.ExpectedReview.ProjectID == "" {
			return nil, ErrInvalidArgument
		}
		r.ProjectID, r.EnvironmentID = request.ExpectedReview.ProjectID, request.ExpectedReview.EnvironmentID
	}
	return r, nil
}
func promotionAttemptError(prior, proposed api.DeploymentPromotionReceipt) error {
	if prior.RequestHash != proposed.RequestHash || prior.DeploymentID != proposed.DeploymentID {
		return ErrDeploymentPromotionAttemptConflict
	}
	return ErrDeploymentPromotionAttemptAccepted
}
func (m *MemStore) DeploymentPromotionReceipt(_ context.Context, appID, attemptID string) (api.DeploymentPromotionReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.deploymentPromotionReceipts[submissionKey(appID, attemptID)]
	app, found := m.apps[appID]
	_, retained := m.deployments[r.DeploymentID]
	if !ok || !found || app.Status == AppDeleted || !retained {
		return api.DeploymentPromotionReceipt{}, ErrNotFound
	}
	if r.Scope != api.DefaultEnvScope {
		env, err := m.workloadSpecEnvironmentLocked(app.AccountID, app.ProjectID, r.Scope, appID)
		if err != nil || env.ID != r.EnvironmentID || app.ProjectID != r.ProjectID {
			return api.DeploymentPromotionReceipt{}, ErrNotFound
		}
	}
	return r, nil
}
func (s *PgStore) DeploymentPromotionReceipt(ctx context.Context, appID, attemptID string) (api.DeploymentPromotionReceipt, error) {
	return readPromotionReceipt(ctx, s.pool, appID, attemptID)
}
func readPromotionReceipt(ctx context.Context, db sqlc.DBTX, appID, attemptID string) (api.DeploymentPromotionReceipt, error) {
	row, err := sqlc.New().ReadDeploymentPromotionReceipt(ctx, db, sqlc.ReadDeploymentPromotionReceiptParams{AppID: appID, AttemptID: attemptID})
	if err != nil {
		return api.DeploymentPromotionReceipt{}, mapErr(err)
	}
	return api.DeploymentPromotionReceipt{AttemptID: row.AttemptID, RequestHash: row.RequestHash, AppID: appID, DeploymentID: row.DeploymentID, ExpectedServingDeploymentID: row.ExpectedServingDeploymentID, Scope: row.Scope, ProjectID: row.ProjectID, EnvironmentID: row.EnvironmentID, AcceptedAt: row.AcceptedAt.Time}, nil
}
func insertPromotionReceipt(ctx context.Context, db sqlc.DBTX, r *api.DeploymentPromotionReceipt) error {
	if r == nil {
		return nil
	}
	return mapErr(sqlc.New().InsertDeploymentPromotionReceipt(ctx, db, sqlc.InsertDeploymentPromotionReceiptParams{AppID: r.AppID, AttemptID: r.AttemptID, RequestHash: r.RequestHash, DeploymentID: r.DeploymentID, ExpectedServingDeploymentID: r.ExpectedServingDeploymentID, Scope: r.Scope, ProjectID: r.ProjectID, EnvironmentID: r.EnvironmentID}))
}
