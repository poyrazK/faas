package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memAutomaticRouteCheck struct {
	FindingBaseline    api.RouteCheckFindingBaseline
	History            [][]byte
	SafetyState        string
	Claim              AutomaticRouteCheckClaim
	CompletedRequestID string
	Record             automaticRouteCheckRecord
}

func (m *MemStore) enqueueAutomaticRouteCheckLocked(appID, deploymentID string, force bool) {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return
	}
	if _, ok := m.savedRouteRequirements[appID]; !ok {
		return
	}
	if m.automaticRouteChecks == nil {
		m.automaticRouteChecks = map[string]memAutomaticRouteCheck{}
	}
	for id, deployment := range m.deployments {
		if deployment.AppID != appID || deploymentID != "" && deploymentID != id {
			continue
		}
		item, exists := m.automaticRouteChecks[id]
		capture, captured := m.openAPIDocs[id]
		if deploymentID == "" && !exists && (!captured || capture.AppID != appID || capture.AccountID != app.AccountID) {
			continue
		}
		if exists && !force && item.CompletedRequestID != item.Claim.RequestID && item.Record.LastErrorCode == "" {
			continue
		}
		now := time.Now().UTC()
		item.Claim = AutomaticRouteCheckClaim{AppID: appID, AccountID: app.AccountID, DeploymentID: id, RequestID: uuid.NewString()}
		item.Record.Version, item.Record.App, item.Record.AppID, item.Record.DeploymentID = 1, app.Slug, appID, id
		item.Record.State, item.Record.Freshness = "pending", "unavailable"
		item.Record.Attempts, item.Record.LastErrorCode = 0, ""
		item.Record.QueuedAt, item.Record.NextAttemptAt = now, &now
		m.automaticRouteChecks[id] = item
	}
}

func (m *MemStore) QueueAutomaticRouteCheck(_ context.Context, accountID, appID, deploymentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.savedRouteRequirementsLocked(accountID, appID); err != nil {
		return err
	}
	deployment, ok := m.deployments[deploymentID]
	if !ok || deployment.AppID != appID {
		return ErrNotFound
	}
	m.enqueueAutomaticRouteCheckLocked(appID, deploymentID, false)
	return nil
}

func (m *MemStore) ClaimAutomaticRouteCheck(_ context.Context, lease time.Duration) (AutomaticRouteCheckClaim, error) {
	if lease <= 0 || lease > api.RouteCheckClaimLease {
		return AutomaticRouteCheckClaim{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var candidates []string
	for id, item := range m.automaticRouteChecks {
		app, ok := m.apps[item.Claim.AppID]
		deployment, exists := m.deployments[id]
		_, saved := m.savedRouteRequirements[item.Claim.AppID]
		if ok && exists && deployment.AppID == app.ID && saved && app.Status != AppDeleted && app.AccountID == item.Claim.AccountID && item.CompletedRequestID != item.Claim.RequestID && !item.Claim.LeaseUntil.After(now) && item.Record.NextAttemptAt != nil && !item.Record.NextAttemptAt.After(now) {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return AutomaticRouteCheckClaim{}, ErrNotFound
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := m.automaticRouteChecks[candidates[i]].Record, m.automaticRouteChecks[candidates[j]].Record
		if !a.NextAttemptAt.Equal(*b.NextAttemptAt) {
			return a.NextAttemptAt.Before(*b.NextAttemptAt)
		}
		if !a.QueuedAt.Equal(b.QueuedAt) {
			return a.QueuedAt.Before(b.QueuedAt)
		}
		return candidates[i] < candidates[j]
	})
	id := candidates[0]
	item := m.automaticRouteChecks[id]
	item.Claim.LeaseToken, item.Claim.LeaseUntil = uuid.NewString(), now.Add(lease)
	item.Claim.Attempts = min(item.Claim.Attempts+1, api.RouteCheckMaxAttempts)
	item.Record.Attempts, item.Record.State = item.Claim.Attempts, "running"
	m.automaticRouteChecks[id] = item
	return item.Claim, nil
}

func (m *MemStore) automaticRouteCheckLeaseLocked(claim AutomaticRouteCheckClaim) (memAutomaticRouteCheck, bool) {
	item, ok := m.automaticRouteChecks[claim.DeploymentID]
	return item, ok && item.Claim.AppID == claim.AppID && item.Claim.AccountID == claim.AccountID && item.Claim.RequestID == claim.RequestID && item.Claim.LeaseToken == claim.LeaseToken && claim.LeaseToken != "" && item.Claim.LeaseUntil.After(time.Now())
}

func (m *MemStore) CompleteAutomaticRouteCheck(_ context.Context, claim AutomaticRouteCheckClaim, check api.RouteRequirementsCheck, captureSHA string, truncated bool) (bool, error) {
	body, err := encodeAutomaticRouteCheck(claim, check, captureSHA, truncated)
	if err != nil {
		return false, err
	}
	var isolated api.RouteRequirementsCheck
	if err := json.Unmarshal(body, &isolated); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, valid := m.automaticRouteCheckLeaseLocked(claim)
	if !valid {
		return false, nil
	}
	now := time.Now().UTC()
	account := m.accounts[claim.AccountID]
	entry, historyBody, baselineBody, err := buildRouteCheckHistory(claim, isolated, item.FindingBaseline, account.MayDeploy() && account.Plan.OpenAPIDocsPerDeployment() > 0, now)
	if err != nil {
		return false, err
	}
	var nextBaseline api.RouteCheckFindingBaseline
	if err := json.Unmarshal(baselineBody, &nextBaseline); err != nil {
		return false, err
	}
	item.FindingBaseline = nextBaseline
	item.History = append(append([][]byte(nil), item.History...), historyBody)
	item.History = pruneMemoryRouteHistory(item.History)
	previousSafety := item.SafetyState
	item.Record.CheckID, item.Record.Changes = claim.RequestID, &entry.Changes
	item.CompletedRequestID, item.Claim.LeaseToken, item.Claim.LeaseUntil = claim.RequestID, "", time.Time{}
	item.Claim.Attempts, item.Record.Attempts, item.Record.LastErrorCode = 0, 0, ""
	item.Record.Check, item.Record.CheckedAt, item.Record.NextAttemptAt = &isolated, &now, nil
	item.Record.State, item.Record.CaptureSHA256, item.Record.CaptureTruncated = "complete", captureSHA, truncated
	if err := m.routeSafetyTransitionLocked(&item, isolated, now); err != nil {
		return false, err
	}
	if err := m.routeFindingTransitionLocked(&item, previousSafety, isolated, now); err != nil {
		return false, err
	}
	m.automaticRouteChecks[claim.DeploymentID] = item
	return true, nil
}

func (m *MemStore) FailAutomaticRouteCheck(_ context.Context, claim AutomaticRouteCheckClaim) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, valid := m.automaticRouteCheckLeaseLocked(claim)
	if !valid {
		return false, nil
	}
	next := time.Now().UTC().Add(routeCheckRetry(item.Claim.Attempts))
	item.Claim.LeaseToken, item.Claim.LeaseUntil = "", time.Time{}
	item.Record.State, item.Record.LastErrorCode, item.Record.NextAttemptAt = "retrying", "check_failed", &next
	m.automaticRouteChecks[claim.DeploymentID] = item
	return true, nil
}

func (m *MemStore) GetAutomaticRouteCheck(_ context.Context, accountID, appID, deploymentID string, fingerprint RouteCheckFingerprinter) (api.AutomaticRouteCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.routePolicySnapshotLocked(accountID, appID)
	if err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	saved, err := m.savedRouteRequirementsLocked(accountID, appID)
	if err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	if err := m.routePolicyContractLocked(&snapshot, deploymentID); err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	item, ok := m.automaticRouteChecks[deploymentID]
	if !ok || item.Claim.AppID != appID || item.Claim.AccountID != accountID {
		return api.AutomaticRouteCheck{}, ErrNotFound
	}
	body, err := json.Marshal(item.Record)
	if err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	var record automaticRouteCheckRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return api.AutomaticRouteCheck{}, err
	}
	record.App = snapshot.App.Slug
	if record.State == "running" && !item.Claim.LeaseUntil.After(time.Now()) {
		record.State = "pending"
	}
	return automaticRouteCheckView(record, snapshot, saved, fingerprint)
}
