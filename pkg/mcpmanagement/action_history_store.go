package mcpmanagement

import (
	"encoding/hex"
	"errors"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// These projections deliberately exclude reasons, routes, source reviews,
// configuration, approval tokens, replay keys and arbitrary native DTO fields.
type actionReceipt struct {
	DeploymentID        string `json:"deployment_id,omitempty"`
	RollbackOperationID string `json:"rollback_operation_id,omitempty"`
	RecoveryRequestID   string `json:"recovery_request_id,omitempty"`
	HandoffGeneration   int64  `json:"handoff_generation,omitempty"`
	RegressionVersion   string `json:"regression_version,omitempty"`
	TriageAction        string `json:"triage_action,omitempty"`
	TriageState         string `json:"triage_state,omitempty"`
}

type actionPhase struct {
	State   string         `json:"state"`
	Receipt *actionReceipt `json:"receipt,omitempty"`
}

type actionRecord struct {
	PlanID        string          `json:"plan_id"`
	Action        string          `json:"action"`
	AccountID     string          `json:"account_id"`
	APIBase       string          `json:"api_base"`
	App           string          `json:"app"`
	AppID         string          `json:"app_id"`
	Scope         string          `json:"scope"`
	Project       string          `json:"project,omitempty"`
	ProjectID     string          `json:"project_id,omitempty"`
	EnvironmentID string          `json:"environment_id,omitempty"`
	RecordedAt    time.Time       `json:"recorded_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	PlanExpiresAt time.Time       `json:"plan_expires_at"`
	RetainedUntil time.Time       `json:"retained_until"`
	Apply         actionPhase     `json:"apply"`
	Promotion     *actionPhase    `json:"promotion,omitempty"`
	Workload      *actionWorkload `json:"workload,omitempty"`
}

type actionHistorySnapshot struct {
	Version int                     `json:"version"`
	Items   map[string]actionRecord `json:"items"`
}

func validActionID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == id
}

func supportedHistoryAction(action string) bool {
	return action == "deploy" || action == "rollback" || action == "regression_action" || workloadHistoryAction(action)
}

func (m *management) restoreActionHistory() error {
	m.actions = make(map[string]actionRecord)
	for _, namespace := range []string{"actions/", workloadActionsNamespace} {
		var saved actionHistorySnapshot
		if err := m.opts.planStore.read(namespace+m.opts.planNamespace, &saved); err != nil {
			return err
		}
		if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
			return errors.New("invalid management action history format or capacity")
		}
		for id, entry := range saved.Items {
			if id != entry.PlanID || validateActionRecord(entry) != nil || workloadHistoryAction(entry.Action) != (namespace == workloadActionsNamespace) || entry.APIBase != m.opts.APIBase || m.opts.accountID != "" && entry.AccountID != m.opts.accountID {
				return errors.New("invalid management action history binding")
			}
			if _, duplicate := m.actions[id]; duplicate {
				return errors.New("duplicate management action history identity")
			}
			m.actions[id] = entry
		}
	}
	if err := m.restoreEnvironmentActionHistory(); err != nil {
		return err
	}
	environmentsBefore := make(map[string]environmentActionRecord, len(m.environmentActions))
	for id, entry := range m.environmentActions {
		environmentsBefore[id] = entry
	}
	before := make(map[string]actionRecord, len(m.actions))
	for id, entry := range m.actions {
		before[id] = entry
	}
	if err := m.syncActionHistory(time.Now().UTC()); err != nil {
		return err
	}
	if !reflect.DeepEqual(before, m.actions) || !reflect.DeepEqual(environmentsBefore, m.environmentActions) {
		// Import only still-live legacy plans. History cannot reconstruct plans
		// already discarded by an older bridge, or mint new replay authority.
		return m.persistPlans()
	}
	return nil
}

// Called with m.mu held, or during construction before the server is exposed.
func (m *management) syncActionHistory(now time.Time) error {
	if m.actions == nil {
		m.actions = make(map[string]actionRecord)
	}
	for _, p := range m.plans {
		if !supportedHistoryAction(p.Action) || !now.Before(p.ExpiresAt.Add(api.ManagementMCPActionHistoryRetention)) {
			continue
		}
		entry := recordAction(p)
		entry.RecordedAt, entry.UpdatedAt = now, now
		if previous, ok := m.actions[p.ID]; ok {
			entry.RecordedAt, entry.UpdatedAt = previous.RecordedAt, previous.UpdatedAt
			if !reflect.DeepEqual(entry, previous) {
				entry.UpdatedAt = now
			}
		}
		if err := validateActionRecord(entry); err != nil {
			return invalid("invalid_response", "action history could not bind the reviewed plan and receipt")
		}
		m.actions[p.ID] = entry
	}
	for id, entry := range m.actions {
		if !now.Before(entry.RetainedUntil) {
			delete(m.actions, id)
		}
	}
	if err := m.syncEnvironmentActionHistory(now); err != nil {
		return err
	}
	m.pruneActionHistories(now)
	return nil
}

func recordAction(p *releasePlan) actionRecord {
	entry := actionRecord{PlanID: p.ID, Action: p.Action, AccountID: p.AccountID, APIBase: p.APIBase, App: p.App, AppID: p.AppID, Scope: p.Scope, Project: p.Project, ProjectID: p.ProjectID, EnvironmentID: p.EnvironmentID, PlanExpiresAt: p.ExpiresAt, RetainedUntil: p.ExpiresAt.Add(api.ManagementMCPActionHistoryRetention), Apply: deploymentActionPhase(p, p.attempted, p.result)}
	if workloadHistoryAction(p.Action) {
		entry.Workload = &actionWorkload{Mode: p.RolloutMode}
		if p.Action == "service_abort" {
			entry.Workload.DeploymentID, entry.Workload.PredecessorDeploymentID = p.TargetDeploymentID, p.PredecessorDeploymentID
		} else {
			entry.Workload.RetainedDeploymentID = p.TargetDeploymentID
		}
		entry.Apply = workloadActionPhase(p)
	}
	if p.Action == "deploy" {
		phase := deploymentActionPhase(p, p.promotionAttempted, p.promoted)
		entry.Promotion = &phase
	}
	if p.Action == "regression_action" && p.Regression != nil && p.regressionResult != nil && validateRegressionReceipt(p.Regression, *p.regressionResult) == nil && validateSegment(p.regressionResult.Version, "version") == nil {
		entry.Apply = actionPhase{State: "committed", Receipt: &actionReceipt{DeploymentID: p.regressionResult.DeploymentID, RegressionVersion: p.regressionResult.Version, TriageAction: p.Regression.Proposed.Action, TriageState: p.regressionResult.State}}
	}
	return entry
}

func deploymentActionPhase(p *releasePlan, attempted bool, d *api.DeploymentResponse) actionPhase {
	phase := actionPhase{State: "not_attempted"}
	if attempted {
		phase.State = "outcome_unknown"
	}
	if d != nil && validateSegment(d.ID, "deployment_id") == nil && d.AppID == p.AppID && normalizedScope(d.Scope) == p.Scope {
		phase = actionPhase{State: "accepted", Receipt: &actionReceipt{DeploymentID: d.ID}}
		if d.RollbackOperation != nil && validateSegment(d.RollbackOperation.ID, "operation_id") == nil {
			phase.Receipt.RollbackOperationID = d.RollbackOperation.ID
		}
	}
	return phase
}

func validateActionRecord(entry actionRecord) error {
	if !validActionID(entry.PlanID) || !supportedHistoryAction(entry.Action) || validateSegment(entry.AccountID, "account_id") != nil || validateSegment(entry.App, "app") != nil || validateSegment(entry.AppID, "app_id") != nil || entry.RecordedAt.IsZero() || entry.UpdatedAt.Before(entry.RecordedAt) || entry.PlanExpiresAt.IsZero() || !entry.RetainedUntil.Equal(entry.PlanExpiresAt.Add(api.ManagementMCPActionHistoryRetention)) || !entry.RecordedAt.Before(entry.RetainedUntil) || !entry.UpdatedAt.Before(entry.RetainedUntil) {
		return errors.New("invalid action identity or retention")
	}
	if entry.Scope != api.DefaultEnvScope && (!api.ValidProjectEnvironmentSlug(entry.Scope) || validateSegment(entry.Project, "project") != nil || validateSegment(entry.ProjectID, "project_id") != nil || validateSegment(entry.EnvironmentID, "environment_id") != nil) || entry.Scope == api.DefaultEnvScope && (entry.Project != "" || entry.ProjectID != "" || entry.EnvironmentID != "") {
		return errors.New("invalid action environment")
	}
	if (entry.Action == "deploy") != (entry.Promotion != nil) {
		return errors.New("invalid action promotion")
	}
	for _, phase := range []*actionPhase{&entry.Apply, entry.Promotion} {
		if phase == nil {
			continue
		}
		switch phase.State {
		case "not_attempted", "outcome_unknown":
			if phase.Receipt != nil {
				return errors.New("unexpected action receipt")
			}
		case "accepted", "committed":
			r := phase.Receipt
			if r == nil || validateSegment(r.DeploymentID, "deployment_id") != nil || r.RollbackOperationID != "" && validateSegment(r.RollbackOperationID, "operation_id") != nil {
				return errors.New("invalid action receipt")
			}
			if entry.Action == "regression_action" {
				state := map[string]string{"acknowledge": "acknowledged", "dismiss": "dismissed", "resolve": "resolved", "reopen": "active"}[r.TriageAction]
				if phase.State != "committed" || state == "" || state != r.TriageState || validateSegment(r.RegressionVersion, "version") != nil || r.RollbackOperationID != "" {
					return errors.New("invalid triage receipt")
				}
			} else if phase.State != "accepted" || r.RegressionVersion != "" || r.TriageAction != "" || r.TriageState != "" || r.RollbackOperationID != "" && entry.Action != "rollback" {
				return errors.New("invalid deployment receipt")
			}
		default:
			return errors.New("invalid action phase")
		}
	}
	return validateWorkloadAction(entry)
}

func actionNewer(a, b actionRecord) bool {
	if !a.RecordedAt.Equal(b.RecordedAt) {
		return a.RecordedAt.After(b.RecordedAt)
	}
	return a.PlanID > b.PlanID
}

func (m *management) actionPlanAvailable(entry actionRecord, now time.Time) bool {
	p := m.plans[entry.PlanID]
	return p != nil && p.Action == entry.Action && p.AccountID == entry.AccountID && p.AppID == entry.AppID && p.EnvironmentID == entry.EnvironmentID && now.Before(p.ExpiresAt)
}
