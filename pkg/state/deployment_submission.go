package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Kept separate from Store for compatibility with unrelated store adapters.
type DeploymentSubmissionStore interface {
	DeploymentSubmission(ctx context.Context, appID, attemptID string) (api.DeploymentSubmissionReceipt, error)
	AttachDeploymentSubmission(ctx context.Context, appID, attemptID, deploymentID string) (api.DeploymentSubmissionReceipt, error)
}

var ErrDeploymentSubmissionConflict = errors.New("deployment attempt already accepted")

type deploymentSubmissionKey struct{}

// ContextWithDeploymentSubmission carries a validated identity from apid to
// deployment creation, including creation through the shared source enqueuer.
func ContextWithDeploymentSubmission(ctx context.Context, attemptID string) context.Context {
	return context.WithValue(ctx, deploymentSubmissionKey{}, attemptID)
}

func deploymentSubmissionAttempt(ctx context.Context) string {
	id, _ := ctx.Value(deploymentSubmissionKey{}).(string)
	return id
}

// DeploymentSubmissionAttempt returns the validated identity carried into creation.
func DeploymentSubmissionAttempt(ctx context.Context) string {
	return deploymentSubmissionAttempt(ctx)
}

func submissionKey(appID, attemptID string) string { return appID + "\x00" + attemptID }

func (m *MemStore) DeploymentSubmission(_ context.Context, appID, attemptID string) (api.DeploymentSubmissionReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	receipt, found := m.deploymentSubmissionReceipts[submissionKey(appID, attemptID)]
	_, retained := m.deployments[receipt.DeploymentID]
	if !ok || app.Status == AppDeleted || !found || !retained {
		return api.DeploymentSubmissionReceipt{}, ErrNotFound
	}
	return receipt, nil
}

func (m *MemStore) AttachDeploymentSubmission(_ context.Context, appID, attemptID, deploymentID string) (api.DeploymentSubmissionReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !api.ValidDeploymentAttemptID(attemptID) {
		return api.DeploymentSubmissionReceipt{}, ErrInvalidArgument
	}
	app, ok := m.apps[appID]
	d, found := m.deployments[deploymentID]
	if !ok || app.Status == AppDeleted || !found || d.AppID != appID {
		return api.DeploymentSubmissionReceipt{}, ErrNotFound
	}
	if prior, exists := m.deploymentSubmissionReceipts[submissionKey(appID, attemptID)]; exists {
		if prior.DeploymentID != deploymentID {
			return api.DeploymentSubmissionReceipt{}, ErrDeploymentSubmissionConflict
		}
		return prior, nil
	}
	return m.saveDeploymentSubmissionLocked(attemptID, d), nil
}

func (m *MemStore) saveDeploymentSubmissionLocked(attemptID string, d Deployment) api.DeploymentSubmissionReceipt {
	if m.deploymentSubmissionReceipts == nil {
		m.deploymentSubmissionReceipts = make(map[string]api.DeploymentSubmissionReceipt)
	}
	receipt := api.DeploymentSubmissionReceipt{AttemptID: attemptID, AppID: d.AppID, DeploymentID: d.ID, AcceptedAt: time.Now().UTC()}
	m.deploymentSubmissionReceipts[submissionKey(d.AppID, attemptID)] = receipt
	return receipt
}

func (s *PgStore) DeploymentSubmission(ctx context.Context, appID, attemptID string) (api.DeploymentSubmissionReceipt, error) {
	row, err := sqlc.New().ReadDeploymentSubmission(ctx, s.pool, sqlc.ReadDeploymentSubmissionParams{AppID: appID, AttemptID: attemptID})
	if err != nil {
		return api.DeploymentSubmissionReceipt{}, mapErr(err)
	}
	return api.DeploymentSubmissionReceipt{AttemptID: row.AttemptID, AppID: appID, DeploymentID: row.DeploymentID, AcceptedAt: row.AcceptedAt.Time}, nil
}

func (s *PgStore) AttachDeploymentSubmission(ctx context.Context, appID, attemptID, deploymentID string) (api.DeploymentSubmissionReceipt, error) {
	if !api.ValidDeploymentAttemptID(attemptID) {
		return api.DeploymentSubmissionReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return api.DeploymentSubmissionReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match creation's app-before-receipt lock order, including FK checks.
	if err := sqlc.New().LockDeploymentSubmissionApp(ctx, tx, appID); err != nil {
		return api.DeploymentSubmissionReceipt{}, mapErr(err)
	}
	receipt, err := attachDeploymentSubmission(ctx, tx, appID, attemptID, deploymentID)
	if err != nil {
		return api.DeploymentSubmissionReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.DeploymentSubmissionReceipt{}, err
	}
	return receipt, nil
}

func attachDeploymentSubmission(ctx context.Context, db sqlc.DBTX, appID, attemptID, deploymentID string) (api.DeploymentSubmissionReceipt, error) {
	if !api.ValidDeploymentAttemptID(attemptID) {
		return api.DeploymentSubmissionReceipt{}, ErrInvalidArgument
	}
	row, err := sqlc.New().AttachDeploymentSubmission(ctx, db, sqlc.AttachDeploymentSubmissionParams{AppID: appID, AttemptID: attemptID, DeploymentID: deploymentID})
	if err != nil {
		return api.DeploymentSubmissionReceipt{}, mapErr(err)
	}
	if row.DeploymentID != deploymentID {
		return api.DeploymentSubmissionReceipt{}, ErrDeploymentSubmissionConflict
	}
	return api.DeploymentSubmissionReceipt{AttemptID: row.AttemptID, AppID: appID, DeploymentID: row.DeploymentID, AcceptedAt: row.AcceptedAt.Time}, nil
}

func (m *MemStore) deleteDeploymentSubmissionsLocked(deploymentID string) {
	for key, r := range m.deploymentPromotionReceipts {
		if r.DeploymentID == deploymentID {
			delete(m.deploymentPromotionReceipts, key)
		}
	}
	delete(m.deploymentCronOutcomes, deploymentID)
	for key, receipt := range m.deploymentSubmissionReceipts {
		if receipt.DeploymentID == deploymentID {
			delete(m.deploymentSubmissionReceipts, key)
		}
	}
}
