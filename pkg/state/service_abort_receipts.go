package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ServiceAbortReceiptStore is optional so older store adapters remain compatible.
type ServiceAbortReceiptStore interface {
	ServiceAbortReceipt(context.Context, string, string) (api.ServiceAbortReceipt, error)
}

// ServiceAbortAttemptStore checks retained acceptance independently of mutable
// environment identity. It exposes no receipt when that identity is unavailable.
type ServiceAbortAttemptStore interface {
	CheckServiceAbortAttempt(context.Context, string, string, string) error
}

var (
	ErrServiceAbortAttemptAccepted = errors.New("service abort attempt already accepted")
	ErrServiceAbortAttemptConflict = errors.New("service abort attempt conflicts with original request")
)

type serviceAbortAttemptKey struct{}

// ContextWithServiceAbortAttempt carries the validated opaque native identity.
// The store derives the request fingerprint itself under its normal locks.
func ContextWithServiceAbortAttempt(ctx context.Context, attempt string) context.Context {
	return context.WithValue(ctx, serviceAbortAttemptKey{}, attempt)
}

func serviceAbortAttempt(ctx context.Context) string {
	id, _ := ctx.Value(serviceAbortAttemptKey{}).(string)
	return id
}

func serviceAbortHash(ctx context.Context, appID, id, predecessorID, reason string) (string, error) {
	review, ok := serviceRecoveryReview(ctx)
	if !api.ValidDeploymentAttemptID(serviceAbortAttempt(ctx)) || !ok || !api.ValidServiceAbortScope(review.Scope, review.ProjectID, review.EnvironmentID) || !sameDeploymentID(review.AppID, appID) {
		return "", ErrInvalidArgument
	}
	return api.ServiceAbortRequestHash(appID, api.RecoverRolloutRequest{Action: "abort", Reason: reason, DeploymentID: id, ExpectedPredecessorDeploymentID: predecessorID, ExpectedReview: &review}), nil
}

func serviceAbortPrior(prior api.ServiceAbortReceipt, hash string) error {
	if prior.RequestHash != hash {
		return ErrServiceAbortAttemptConflict
	}
	return ErrServiceAbortAttemptAccepted
}

func serviceAbortReceiptData(ctx context.Context, d Deployment, reason string) []byte {
	data := map[string]any{
		"action": "abort_requested", "reason": normalizeRolloutReason(reason),
		"deployment_id": d.ID, "predecessor_deployment_id": d.ServiceRolloutHandoff.PredecessorDeploymentID,
		"request_id": d.ServiceRolloutHandoff.BindingsCheck.RequestID,
	}
	if attempt := serviceAbortAttempt(ctx); attempt != "" {
		hash, _ := serviceAbortHash(ctx, d.AppID, d.ID, d.ServiceRolloutHandoff.PredecessorDeploymentID, reason)
		receipt := api.ServiceAbortReceipt{
			AttemptID: attempt, RequestHash: hash, AppID: uuid.MustParse(d.AppID).String(),
			DeploymentID: uuid.MustParse(d.ID).String(), PredecessorDeploymentID: uuid.MustParse(d.ServiceRolloutHandoff.PredecessorDeploymentID).String(),
			RequestID: d.ServiceRolloutHandoff.BindingsCheck.RequestID, Scope: normalizedDeploymentScope(d.Scope), AcceptedAt: time.Now().UTC(),
		}
		if receipt.Scope != api.DefaultEnvScope {
			review, _ := serviceRecoveryReview(ctx)
			receipt.ProjectID, receipt.EnvironmentID = uuid.MustParse(review.ProjectID).String(), uuid.MustParse(review.EnvironmentID).String()
		}
		data["service_abort_receipt"] = receipt
	}
	payload, _ := json.Marshal(data)
	return payload
}

func (m *MemStore) ServiceAbortReceipt(_ context.Context, appID, attempt string) (api.ServiceAbortReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serviceAbortReceiptLocked(appID, attempt)
}

func (m *MemStore) serviceAbortReceiptLocked(appID, attempt string) (api.ServiceAbortReceipt, error) {
	r, err := m.serviceAbortAcceptanceLocked(appID, attempt)
	if err != nil {
		return r, err
	}
	app := m.apps[appID]
	if r.Scope != api.DefaultEnvScope {
		env, err := m.workloadSpecEnvironmentLocked(app.AccountID, app.ProjectID, r.Scope, appID)
		if err != nil || !sameDeploymentID(env.ID, r.EnvironmentID) || !sameDeploymentID(app.ProjectID, r.ProjectID) {
			return api.ServiceAbortReceipt{}, ErrNotFound
		}
	}
	for _, d := range m.deployments {
		if sameDeploymentID(d.ID, r.DeploymentID) && d.AppID == appID && normalizedDeploymentScope(d.Scope) == r.Scope {
			return r, nil
		}
	}
	return api.ServiceAbortReceipt{}, ErrNotFound
}

func (m *MemStore) serviceAbortAcceptanceLocked(appID, attempt string) (api.ServiceAbortReceipt, error) {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return api.ServiceAbortReceipt{}, ErrNotFound
	}
	for _, row := range m.deploymentAudit {
		var data struct {
			Receipt *api.ServiceAbortReceipt `json:"service_abort_receipt"`
		}
		if err := json.Unmarshal(row.Data, &data); err != nil || data.Receipt == nil {
			continue
		}
		r := *data.Receipt
		if r.AttemptID != attempt || !sameDeploymentID(r.AppID, appID) {
			continue
		}
		return r, nil
	}
	return api.ServiceAbortReceipt{}, ErrNotFound
}

func (m *MemStore) CheckServiceAbortAttempt(_ context.Context, appID, attempt, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prior, err := m.serviceAbortAcceptanceLocked(appID, attempt)
	if err != nil {
		return err
	}
	return serviceAbortPrior(prior, hash)
}

func (s *PgStore) CheckServiceAbortAttempt(ctx context.Context, appID, attempt, hash string) error {
	prior, err := readServiceAbortAcceptance(ctx, s.pool, appID, attempt)
	if err != nil {
		return err
	}
	return serviceAbortPrior(prior, hash)
}

func readServiceAbortAcceptance(ctx context.Context, db sqlc.DBTX, appID, attempt string) (api.ServiceAbortReceipt, error) {
	data, err := sqlc.New().ReadServiceAbortAcceptance(ctx, db, sqlc.ReadServiceAbortAcceptanceParams{AppID: appID, AttemptID: attempt})
	if err != nil {
		return api.ServiceAbortReceipt{}, mapErr(err)
	}
	var receipt api.ServiceAbortReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return api.ServiceAbortReceipt{}, err
	}
	return receipt, nil
}

func (s *PgStore) ServiceAbortReceipt(ctx context.Context, appID, attempt string) (api.ServiceAbortReceipt, error) {
	return readServiceAbortReceipt(ctx, s.pool, appID, attempt)
}

func readServiceAbortReceipt(ctx context.Context, db sqlc.DBTX, appID, attempt string) (api.ServiceAbortReceipt, error) {
	data, err := sqlc.New().ReadServiceAbortReceipt(ctx, db, sqlc.ReadServiceAbortReceiptParams{AppID: appID, AttemptID: attempt})
	if err != nil {
		return api.ServiceAbortReceipt{}, mapErr(err)
	}
	var receipt api.ServiceAbortReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return api.ServiceAbortReceipt{}, err
	}
	return receipt, nil
}
