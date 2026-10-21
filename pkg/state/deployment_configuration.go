package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type DeploymentConfigurationStore interface {
	DeploymentConfiguration(context.Context, string, string) (api.DeploymentConfigurationStatus, error)
}
type cronOutcome struct {
	SessionID      string
	Revision       int64
	Status, Reason string
}

func cronDispositionReason(status, scope, rollout string, traffic, steps int, explicit, competing bool, id, latest string) string {
	switch status {
	case "failed":
		return "deployment_failed"
	case "cancelled", "canceled":
		return "deployment_canceled"
	case "superseded":
		return "deployment_superseded"
	}
	if rollout == "aborted" || status == "aborted" {
		return "rollout_aborted"
	}
	if scope != "" && scope != DefaultEnvScope || explicit && traffic == 0 && steps == 0 {
		return "non_production"
	}
	if id != latest {
		return "deployment_superseded"
	}
	if status != "live" {
		return "awaiting_readiness"
	}
	if traffic != 100 {
		return "awaiting_full_traffic"
	}
	if competing {
		return "competing_traffic"
	}
	if rollout == "rolling_out" || steps > 0 && rollout != "complete" {
		return "awaiting_rollout"
	}
	return "awaiting_activation"
}
func configurationStatus(status, reason, release, scope, rollout string, traffic, steps int, explicit, competing bool, id, latest string) api.DeploymentConfigurationStatus {
	if status == "canceled" {
		status, reason = "discarded", "activation_discarded"
	}
	if status == "prepared" {
		reason = cronDispositionReason(release, scope, rollout, traffic, steps, explicit, competing, id, latest)
		if cronDeploymentDisposition(release, scope, rollout, traffic, steps, explicit, competing, id, latest) == "discarded" {
			status = "discarded"
		}
	}
	if status == "conflict" && reason == "" {
		reason = "cron_guard_conflict"
	}
	if status == "discarded" && reason == "" {
		reason = "activation_discarded"
	}
	return api.NewDeploymentConfigurationStatus(status, reason)
}
func (m *MemStore) cronActivationDeploymentLocked(appID string, a cronActivation) (Deployment, bool) {
	id := m.deploymentSubmissionReceipts[submissionKey(appID, a.Intent.SessionID)].DeploymentID
	if id == "" {
		if u, ok := m.uploadSessions[a.UploadID]; ok && u.Status == "committed" && u.DeploymentID.Valid {
			id = u.DeploymentID.String
		}
	}
	for key, d := range m.deployments {
		if d.AppID == appID && sameDeploymentID(key, id) {
			return d, true
		}
	}
	return Deployment{}, false
}
func (m *MemStore) recordCronOutcomeLocked(appID string, a cronActivation, status, reason string) {
	d, ok := m.cronActivationDeploymentLocked(appID, a)
	if !ok {
		return
	}
	if m.deploymentCronOutcomes == nil {
		m.deploymentCronOutcomes = map[string]cronOutcome{}
	}
	prior := m.deploymentCronOutcomes[d.ID]
	if prior.Revision > a.Revision {
		return
	}
	m.deploymentCronOutcomes[d.ID] = cronOutcome{SessionID: a.Intent.SessionID, Revision: a.Revision, Status: status, Reason: reason}
}
func (m *MemStore) archiveCronOutcomeLocked(appID string, a cronActivation) {
	status, reason := a.Status, ""
	if status == "canceled" {
		status, reason = "discarded", "activation_discarded"
	}
	if d, ok := m.cronActivationDeploymentLocked(appID, a); ok {
		if h := m.deploymentCronOutcomes[d.ID]; h.Revision == a.Revision {
			reason = h.Reason
		}
	}
	if status == "prepared" {
		status, reason = "discarded", "intent_superseded"
	}
	if status == "conflict" && reason == "" {
		reason = "cron_guard_conflict"
	}
	m.recordCronOutcomeLocked(appID, a, status, reason)
}
func (m *MemStore) cronProductionContextLocked(d Deployment) (string, bool) {
	latest := ""
	var newest Deployment
	competing := false
	for _, candidate := range m.deployments {
		if candidate.AppID != d.AppID || normalizedDeploymentScope(candidate.Scope) != DefaultEnvScope {
			continue
		}
		if candidate.ID != d.ID && candidate.Status == DeployLive && candidate.TrafficPercent > 0 {
			competing = true
		}
		if candidate.TrafficPercentExplicit && candidate.TrafficPercent == 0 && candidate.CanaryTotalSteps == 0 {
			continue
		}
		if latest == "" || candidate.CreatedAt.After(newest.CreatedAt) || candidate.CreatedAt.Equal(newest.CreatedAt) && candidate.ID > newest.ID {
			newest = candidate
			latest = candidate.ID
		}
	}
	return latest, competing
}
func (m *MemStore) DeploymentConfiguration(_ context.Context, accountID, deploymentID string) (api.DeploymentConfigurationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	app, owned := m.apps[d.AppID]
	if !ok || !owned || app.AccountID != accountID || app.Status == AppDeleted {
		return api.DeploymentConfigurationStatus{}, ErrNotFound
	}
	h := m.deploymentCronOutcomes[d.ID]
	if a, ok := m.deploymentCronActivations[d.AppID]; ok {
		if bound, ok := m.cronActivationDeploymentLocked(d.AppID, a); ok && bound.ID == d.ID && a.Revision >= h.Revision {
			h.Status = a.Status
			h.SessionID = a.Intent.SessionID
			if h.Revision != a.Revision {
				h.Reason = ""
			}
			h.Revision = a.Revision
		}
	}
	latest, competing := m.cronProductionContextLocked(d)
	result := configurationStatus(h.Status, h.Reason, string(d.Status), d.Scope, d.RolloutState, d.TrafficPercent, d.CanaryTotalSteps, d.TrafficPercentExplicit, competing, d.ID, latest)
	result.Crons.SessionID, result.Crons.Revision = h.SessionID, h.Revision
	return result, nil
}
func (s *PgStore) DeploymentConfiguration(ctx context.Context, accountID, deploymentID string) (api.DeploymentConfigurationStatus, error) {
	r, err := sqlc.New().ReadDeploymentConfiguration(ctx, s.pool, sqlc.ReadDeploymentConfigurationParams{AccountID: accountID, DeploymentID: deploymentID})
	if err != nil {
		return api.DeploymentConfigurationStatus{}, mapErr(err)
	}
	result := configurationStatus(r.ActivationStatus, r.Reason, r.ReleaseStatus, r.Scope, r.RolloutState, int(r.TrafficPercent), int(r.CanaryTotalSteps), r.TrafficPercentExplicit, r.CompetingTraffic, r.DeploymentID, r.LatestID)
	result.Crons.SessionID, result.Crons.Revision = r.SessionID, r.Revision
	return result, nil
}

func cronActivationConflictReason(err error) string {
	var quota *CronQuotaError
	if errors.As(err, &quota) {
		return "quota_changed"
	}
	return "identity_conflict"
}
