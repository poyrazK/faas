package state

import (
	"context"
	"errors"
)

var _ RegionalImageReleaseStore = (*MemStore)(nil)

func (m *MemStore) RegionalImageReleaseReceipt(_ context.Context, cellID, appID, releaseID string) (RegionalImageReleaseReceipt, error) {
	if !validRegionalReleaseIDs(cellID, appID, releaseID) {
		return RegionalImageReleaseReceipt{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, err := m.regionalImageReleaseReceiptLocked(cellID, appID, releaseID)
	if err != nil {
		return receipt, err
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != receipt.AccountID || !m.regionalReleasePersonalApp(app) {
		return RegionalImageReleaseReceipt{}, ErrNotFound
	}
	return receipt, nil
}

func (m *MemStore) regionalImageReleaseReceiptLocked(cellID, appID, releaseID string) (RegionalImageReleaseReceipt, error) {
	receipt, ok := m.regionalImageReleaseReceipts[appID+"/"+releaseID]
	if !ok || receipt.CellID != cellID {
		return RegionalImageReleaseReceipt{}, ErrNotFound
	}
	if d, ok := m.deployments[receipt.DeploymentID]; ok {
		receipt.DeploymentStatus = string(d.Status)
	} else {
		receipt.DeploymentID, receipt.DeploymentStatus = "", "expired"
	}
	return receipt, nil
}

func (m *MemStore) CreateRegionalImageRelease(_ context.Context, intent RegionalImageReleaseIntent, d Deployment, activity *OrgActivity) (RegionalImageReleaseResult, error) {
	if err := validateRegionalImageRelease(intent, d); err != nil {
		return RegionalImageReleaseResult{}, err
	}
	if activity != nil {
		copy := *activity
		activity = &copy
	}
	mutation := &regionalImageReleaseMutation{intent: intent}
	created, outboxID, err := m.createDeployment(d, activity, mutation)
	return RegionalImageReleaseResult{Receipt: mutation.receipt, Deployment: created, ActivityOutboxID: outboxID, Replayed: mutation.replayed}, err
}

// Caller holds m.mu, matching the PostgreSQL app-row lock.
func (m *MemStore) lockRegionalImageRelease(app App, mutation *regionalImageReleaseMutation) error {
	i := mutation.intent
	if app.AccountID != i.AccountID || app.OrgID != i.OrgID || (app.Status != AppActive && app.Status != AppEvictedCold) {
		return ErrNotFound
	}
	if !m.regionalReleasePersonalApp(app) {
		return ErrNotFound
	}
	receipt, err := m.regionalImageReleaseReceiptLocked(i.CellID, i.AppID, i.ReleaseID)
	if errors.Is(err, ErrNotFound) {
		// A release cannot be rebound to another cell even in the in-memory store.
		if _, exists := m.regionalImageReleaseReceipts[i.AppID+"/"+i.ReleaseID]; exists {
			return ErrNotFound
		}
		if head, exists := m.regionalImageReleaseHeads[i.AppID]; exists && i.GlobalAppID != "" {
			return mutation.checkHead(head)
		}
		return nil
	}
	if err != nil {
		return err
	}
	return mutation.replay(receipt)
}

// Caller holds m.mu. Receipt replay observes current personal ownership.
func (m *MemStore) regionalReleasePersonalApp(app App) bool {
	if app.Status != AppActive && app.Status != AppEvictedCold {
		return false
	}
	if app.OrgID == "" {
		return true
	}
	org, ok := m.orgs[app.OrgID]
	return ok && org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == app.AccountID && org.Status == OrgStatusActive && !org.DeletedPending
}
