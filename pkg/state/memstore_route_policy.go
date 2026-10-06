package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) routePolicySnapshotLocked(accountID, appID string) (RoutePolicySnapshot, error) {
	account, ok := m.accounts[accountID]
	if !ok {
		return RoutePolicySnapshot{}, ErrNotFound
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return RoutePolicySnapshot{}, ErrNotFound
	}
	snapshot := RoutePolicySnapshot{Account: account, App: app, Rules: []api.EdgeRuleResponse{}}
	for _, rule := range m.edgeRules {
		if rule.AppID != appID {
			continue
		}
		action, _ := json.Marshal(rule.Action)
		mode := rule.ValidateMode
		if mode == "" {
			mode = api.ValidateModeBlock
		}
		snapshot.Rules = append(snapshot.Rules, api.EdgeRuleResponse{ID: rule.ID, AccountID: rule.AccountID, AppID: rule.AppID,
			MatchHost: rule.MatchHost, MatchPath: rule.MatchPath, MatchMethods: rule.MatchMethods, MatchHeaders: rule.MatchHeaders,
			Priority: rule.Priority, Enabled: rule.Enabled, Kind: string(rule.Kind), Action: action, ValidateMode: mode,
			CreatedAt: rule.CreatedAt, UpdatedAt: rule.UpdatedAt})
	}
	// Prevent callbacks from aliasing mutable maps, slices, and policy pointers.
	body, err := json.Marshal(snapshot)
	if err != nil {
		return RoutePolicySnapshot{}, err
	}
	var isolated RoutePolicySnapshot
	err = json.Unmarshal(body, &isolated)
	return isolated, err
}

func (m *MemStore) PlanRoutePolicy(_ context.Context, accountID, appID string, request api.RoutePolicyPlanRequest, planner RoutePolicyPlanner) (api.RoutePolicyPlan, error) {
	if err := ValidateRoutePolicySource(request); err != nil {
		return api.RoutePolicyPlan{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.routePolicySnapshotLocked(accountID, appID)
	if err != nil {
		return api.RoutePolicyPlan{}, err
	}
	if err := m.routePolicyContractLocked(&snapshot, request.DeploymentID); err != nil {
		return api.RoutePolicyPlan{}, err
	}
	if err := m.routePolicySavedRequirementsLocked(&snapshot, request); err != nil {
		return api.RoutePolicyPlan{}, err
	}
	return planner(snapshot)
}

func routePolicyReceiptKey(accountID, appID, key string) string {
	return accountID + "\x00" + appID + "\x00" + key
}

func (m *MemStore) findRoutePolicyReceiptLocked(accountID, appID, key string, request api.RoutePolicyApplyRequest) (api.RoutePolicyReceipt, error) {
	stored, ok := m.routePolicyReceipts[routePolicyReceiptKey(accountID, appID, key)]
	if !ok {
		return api.RoutePolicyReceipt{}, ErrNotFound
	}
	if stored.RequestSHA256 != RoutePolicyRequestSHA256(request) {
		return api.RoutePolicyReceipt{}, ErrRoutePolicyKeyReused
	}
	return copyRoutePolicyReceipt(stored.Receipt), nil
}

func (m *MemStore) FindRoutePolicyReceipt(_ context.Context, accountID, appID, key string, request api.RoutePolicyApplyRequest) (api.RoutePolicyReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.routePolicySnapshotLocked(accountID, appID); err != nil {
		return api.RoutePolicyReceipt{}, err
	}
	return m.findRoutePolicyReceiptLocked(accountID, appID, key, request)
}

func (m *MemStore) ApplyRoutePolicy(_ context.Context, accountID, appID, key string, request api.RoutePolicyApplyRequest, planner RoutePolicyPlanner) (api.RoutePolicyReceipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ValidateRoutePolicyApply(key, request); err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	snapshot, err := m.routePolicySnapshotLocked(accountID, appID)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	if receipt, err := m.findRoutePolicyReceiptLocked(accountID, appID, key, request); err == nil || errors.Is(err, ErrRoutePolicyKeyReused) {
		return receipt, err == nil, err
	}
	if err := m.routePolicyContractLocked(&snapshot, request.DeploymentID); err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	if err := m.routePolicySavedRequirementsLocked(&snapshot, request.RoutePolicyPlanRequest); err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	plan, err := routePolicyPlanForApply(snapshot, request, planner)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	now := m.clock().UTC()
	staged, changes, err := stageRoutePolicy(snapshot, plan, now)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	receipt, err := verifyRoutePolicy(staged, planner, plan, changes, now)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	rules := make(map[string]EdgeRule, len(changes))
	for _, change := range changes {
		for _, proposed := range staged.Rules {
			if proposed.ID != change.RuleID {
				continue
			}
			rule := m.edgeRules[proposed.ID]
			if change.Operation == "create" {
				rule = EdgeRule{ID: proposed.ID, AccountID: accountID, AppID: appID, Kind: EdgeRuleKind(proposed.Kind),
					MatchHost: proposed.MatchHost, MatchPath: proposed.MatchPath, MatchMethods: proposed.MatchMethods,
					Priority: proposed.Priority, Enabled: true, ValidateMode: api.ValidateModeBlock, CreatedAt: now}
			}
			rule.Action = EdgeRuleAction{}
			if err := json.Unmarshal(proposed.Action, &rule.Action); err != nil {
				return api.RoutePolicyReceipt{}, false, fmt.Errorf("decode planned action: %w", err)
			}
			rule.UpdatedAt = now
			rules[rule.ID] = rule
		}
	}
	for id, rule := range rules {
		m.edgeRules[id] = rule
	}
	if len(changes) > 0 {
		m.enqueueRoutePolicyChecksLocked(appID)
	}
	if m.routePolicyReceipts == nil {
		m.routePolicyReceipts = map[string]routePolicyStoredReceipt{}
	}
	m.routePolicyReceipts[routePolicyReceiptKey(accountID, appID, key)] = routePolicyStoredReceipt{RoutePolicyRequestSHA256(request), copyRoutePolicyReceipt(receipt)}
	return receipt, false, nil
}

func (m *MemStore) routePolicySavedRequirementsLocked(snapshot *RoutePolicySnapshot, request api.RoutePolicyPlanRequest) error {
	if !request.Saved {
		return nil
	}
	saved, err := m.savedRouteRequirementsLocked(snapshot.Account.ID, snapshot.App.ID)
	if err != nil {
		return err
	}
	return bindRoutePolicySavedRequirements(snapshot, request, saved)
}

func (m *MemStore) GetRoutePolicyReceipt(_ context.Context, accountID, appID, id string) (api.RoutePolicyReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.routePolicySnapshotLocked(accountID, appID); err != nil {
		return api.RoutePolicyReceipt{}, err
	}
	for _, stored := range m.routePolicyReceipts {
		if stored.Receipt.ID == id && stored.Receipt.AppID == appID {
			return copyRoutePolicyReceipt(stored.Receipt), nil
		}
	}
	return api.RoutePolicyReceipt{}, ErrNotFound
}

func (m *MemStore) routePolicyContractLocked(snapshot *RoutePolicySnapshot, deploymentID string) error {
	if deploymentID == "" {
		return nil
	}
	deployment, ok := m.deployments[deploymentID]
	if !ok || deployment.AppID != snapshot.App.ID {
		return ErrNotFound
	}
	contract := &RoutePolicyContract{DeploymentID: deploymentID}
	if row, ok := m.openAPIDocs[deploymentID]; ok && row.AccountID == snapshot.Account.ID && row.AppID == snapshot.App.ID {
		contract.Doc = append([]byte(nil), row.Doc...)
		contract.SHA256 = fmt.Sprintf("%x", row.DocSHA256)
		contract.Truncated = row.Truncated
	}
	snapshot.Contract = contract
	return nil
}
