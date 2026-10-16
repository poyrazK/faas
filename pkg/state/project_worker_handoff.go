package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) workerGraphAdmissionLocked(appID, deploymentID string) error {
	app := m.apps[appID]
	dep := m.deployments[deploymentID]
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		return nil
	}
	graph := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(app.ProjectID, dep.Scope)]]
	if graph.ID == "" {
		return nil
	}
	for _, member := range graph.Members {
		if member.AppID == appID && member.DeploymentID == deploymentID {
			return nil
		}
	}
	// Ordinary priming remains a qualification path. It cannot authorize a
	// new resident for a live revision outside the active graph.
	if dep.Status == DeploySnapshotting || dep.Status == DeployImaging {
		return nil
	}
	if m.promotionWorkerHeldLocked(appID, deploymentID) {
		return nil
	}
	if m.pinnedProjectWorkerLocked(appID, deploymentID) {
		return nil
	}
	if attempt, ok := m.releaseWorkerCandidates[deploymentID]; ok {
		entry := m.projectSubmissions[app.AccountID+"\x00"+attempt]
		var receipt apiReceiptForWorkerHold
		if json.Unmarshal(entry.Payload, &receipt) == nil && receipt.Apply.SafeRelease.Status == "building" && receipt.Apply.SafeRelease.Deadline.After(time.Now()) {
			return nil
		}
	}
	return ErrConflict
}

// Keep the read projection narrow; it carries no application values.
type apiReceiptForWorkerHold struct {
	Apply struct {
		SafeRelease struct {
			Status   string
			Deadline time.Time
		} `json:"safe_release"`
	} `json:"apply"`
}

func workerGraphAdmissionDB(ctx context.Context, db sqlc.DBTX, appID, deploymentID string) error {
	if err := sqlc.New().LockProjectWorkerHandoff(ctx, db, appID); err != nil {
		return err
	}
	allowed, err := sqlc.New().ProjectWorkerGraphAdmission(ctx, db, sqlc.ProjectWorkerGraphAdmissionParams{AppID: appID, DeploymentID: deploymentID})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) ClaimProjectWorkerDrain(_ context.Context, appID, instanceID, expectedTarget string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	ins, found := m.instances[instanceID]
	if !ok || !found || ins.AppID != appID || ins.Mode != string(InstanceModeWorker) {
		return false, ErrNotFound
	}
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		return true, nil
	}
	graph := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(app.ProjectID, m.deployments[ins.DeploymentID].Scope)]]
	selected := ""
	for _, member := range graph.Members {
		if member.AppID == appID {
			selected = member.DeploymentID
		}
	}
	if app.Status == AppActive && (selected != "" && selected != expectedTarget || selected == ins.DeploymentID) {
		return false, nil
	}
	if app.Status == AppActive {
		if m.promotionWorkerHeldLocked(appID, ins.DeploymentID) {
			return false, nil
		}
		if m.pinnedProjectWorkerLocked(appID, ins.DeploymentID) {
			redundant := false
			for _, sibling := range m.instances {
				if sibling.ID != ins.ID && sibling.AppID == appID && sibling.DeploymentID == ins.DeploymentID && sibling.Mode == string(InstanceModeWorker) && sibling.State == string(StateRunning) && !m.releaseWorkerDrains[sibling.ID] {
					redundant = true
					break
				}
			}
			if !redundant {
				return false, nil
			}
		}
		if attempt, exists := m.releaseWorkerCandidates[ins.DeploymentID]; exists {
			entry := m.projectSubmissions[app.AccountID+"\x00"+attempt]
			var receipt apiReceiptForWorkerHold
			if json.Unmarshal(entry.Payload, &receipt) == nil && receipt.Apply.SafeRelease.Status == "building" && receipt.Apply.SafeRelease.Deadline.After(time.Now()) {
				return false, nil
			}
		}
	}
	if m.releaseWorkerDrains == nil {
		m.releaseWorkerDrains = map[string]bool{}
	}
	m.releaseWorkerDrains[instanceID] = true
	return true, nil
}
func (s *PgStore) ClaimProjectWorkerDrain(ctx context.Context, appID, instanceID, expectedTarget string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockProjectWorkerHandoff(ctx, tx, appID); err != nil {
		return false, err
	}
	claimed, err := q.ClaimProjectWorkerDrain(ctx, tx, sqlc.ClaimProjectWorkerDrainParams{AppID: appID, InstanceID: instanceID, ExpectedTarget: expectedTarget})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return claimed, nil
}
