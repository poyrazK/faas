package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// This authorization is internal and transaction-local. HTTP bodies cannot
// supply it. The database rejects every unfenced production traffic increase.
func pgAuthorizeProductionLifecycle(ctx context.Context, tx pgx.Tx, id string, recovery bool) error {
	snapshot, err := pgLockCanaryRouteSnapshot(ctx, tx, id)
	if err != nil {
		return err
	}
	gate, err := pgCanaryRouteGate(ctx, tx, snapshot.Account.ID, snapshot.App.ID)
	if err != nil {
		return err
	}
	saved, err := pgSavedRouteRequirements(ctx, tx, snapshot.Account.ID, snapshot.App.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	q := sqlc.New()
	ids := []string{id}
	type grant struct {
		DeploymentID string                `json:"deployment_id"`
		Inputs       json.RawMessage       `json:"inputs"`
		ApprovalIDs  []string              `json:"approval_ids"`
		Decision     api.RouteGateDecision `json:"decision"`
		Recovery     bool                  `json:"recovery"`
		Scope        string                `json:"scope"`
	}
	grants := []grant{}
	previous, err := q.ReadLifecycleTrafficFences(ctx, tx)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(previous, &grants); err != nil {
		return err
	}
	originalCount := len(grants)
	for _, candidateID := range ids {
		scope, err := q.ReadLifecycleDeploymentScope(ctx, tx, sqlc.ReadLifecycleDeploymentScopeParams{DeploymentID: candidateID, AppID: snapshot.App.ID})
		if err != nil {
			return err
		}
		if !routeRemovalProductionScope(scope) {
			continue
		}
		decision := api.RouteGateDecision{Mode: gate.Mode, Revision: gate.Revision, DeploymentID: candidateID, Status: "allowed", Reasons: []string{}}
		if gate.Mode == "report" {
			decision.Status = "report_only"
		}
		if !recovery {
			candidate := snapshot
			if err := pgRoutePolicyContract(ctx, tx, &candidate, candidateID, true); err != nil {
				return err
			}
			sha, readErr := q.ReadLifecycleConfigurationReceipt(ctx, tx, sqlc.ReadLifecycleConfigurationReceiptParams{AppID: snapshot.App.ID, DeploymentID: candidateID})
			if readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
				return readErr
			}
			fingerprint := RouteCheckFingerprinter(func(RoutePolicySnapshot) string { return sha })
			if err := pgLifecycleGate(ctx, tx, candidate, Deployment{ID: candidateID, Scope: scope}, &decision, time.Now().UTC(), saved, fingerprint); err != nil {
				return err
			}
			if decision.Status == "blocked" {
				return &RouteGateBlockedError{Decision: decision}
			}
		}
		grants = append(grants, grant{DeploymentID: candidateID, ApprovalIDs: decision.LifecycleApprovalIDs, Decision: decision, Recovery: recovery, Scope: normalizedDeploymentScope(scope)})
	}
	inputs, err := q.ReadLifecycleTrafficInputs(ctx, tx, snapshot.App.ID)
	if err != nil {
		return err
	}
	for i := originalCount; i < len(grants); i++ {
		grants[i].Inputs = inputs
	}
	body, err := json.Marshal(grants)
	if err != nil {
		return err
	}
	_, err = q.AuthorizeLifecycleTraffic(ctx, tx, string(body))
	return err
}

func lifecycleMemoryConfiguration(snapshot RoutePolicySnapshot) string {
	rules := append([]api.EdgeRuleResponse(nil), snapshot.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	// Exclude captures and saved requirements: their hashes/revisions are bound
	// independently. Conservatively bind all application/account configuration.
	body, _ := json.Marshal(struct {
		Account Account
		App     App
		Rules   []api.EdgeRuleResponse
	}{snapshot.Account, snapshot.App, rules})
	return string(body)
}

func (m *MemStore) checkProductionLifecycleLocked(proposed map[string]int) error {
	for id, percent := range proposed {
		d := m.deployments[id]
		delete(m.productionLifecycleDecisions, id)
		if m.lifecycleRecovery || percent <= 0 || !routeRemovalProductionScope(d.Scope) || d.Status == DeployLive && percent <= d.TrafficPercent {
			continue
		}
		app := m.apps[d.AppID]
		gate, err := m.canaryRouteGateLocked(app.AccountID, app.ID)
		if err != nil {
			return err
		}
		snapshot, err := m.routePolicySnapshotLocked(app.AccountID, app.ID)
		if err != nil {
			return err
		}
		saved, err := m.savedRouteRequirementsLocked(app.AccountID, app.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		configuration := lifecycleMemoryConfiguration(snapshot)
		sha := ""
		latest := time.Time{}
		now := time.Now().UTC()
		for receiptID, receipt := range m.routeLifecycleApprovals {
			if receipt.CandidateDeploymentID == id && receipt.InvalidatedAt == nil && now.Before(receipt.ValidUntil) && receipt.ApprovedAt.After(latest) && m.lifecycleApprovalConfigurations[receiptID] == configuration {
				sha = receipt.ConfigurationSHA256
				latest = receipt.ApprovedAt
			}
		}
		if err := m.routePolicyContractLocked(&snapshot, id); err != nil {
			return err
		}
		decision := api.RouteGateDecision{Mode: gate.Mode, Revision: gate.Revision, DeploymentID: id, Status: "allowed", Reasons: []string{}}
		if gate.Mode == "report" {
			decision.Status = "report_only"
		}
		if err := m.lifecycleGateLocked(snapshot, d, &decision, time.Now().UTC(), saved, func(RoutePolicySnapshot) string { return sha }); err != nil {
			return err
		}
		if decision.Status == "blocked" {
			m.recordLifecycleHistoryLocked(d, decision, false, "blocked")
			return &RouteGateBlockedError{Decision: decision}
		}
		if m.productionLifecycleDecisions == nil {
			m.productionLifecycleDecisions = map[string]api.RouteGateDecision{}
		}
		m.productionLifecycleDecisions[id] = decision
	}
	return nil
}

func pgAuthorizeLifecycleActivation(ctx context.Context, tx pgx.Tx, id string) error {
	dark, err := sqlc.New().ReadLifecycleDarkActivation(ctx, tx, id)
	if err != nil {
		return routePolicyReadError(err)
	}
	if dark {
		return nil
	}
	return pgAuthorizeProductionLifecycle(ctx, tx, id, false)
}

func pgLockProductionLifecycleApp(ctx context.Context, tx pgx.Tx, appID string) error {
	accountID, err := sqlc.New().ReadLifecycleAppOwner(ctx, tx, appID)
	if err != nil {
		return routePolicyReadError(err)
	}
	_, err = pgRoutePolicySnapshot(ctx, tx, accountID, appID, true)
	return err
}
