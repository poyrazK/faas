package mcpmanagement

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const environmentActionsNamespace = "environment-actions/"

type environmentActionReceipt struct {
	PromotionID string `json:"promotion_id"`
}

type environmentActionPhase struct {
	State   string                    `json:"state"`
	Receipt *environmentActionReceipt `json:"receipt,omitempty"`
}

type environmentActionRecord struct {
	PlanID            string                 `json:"plan_id"`
	Action            string                 `json:"action"`
	AccountID         string                 `json:"account_id"`
	APIBase           string                 `json:"api_base"`
	Project           string                 `json:"project"`
	ProjectID         string                 `json:"project_id"`
	FromEnvironment   string                 `json:"from_environment"`
	FromID            string                 `json:"from_environment_id"`
	Environment       string                 `json:"environment"`
	EnvironmentID     string                 `json:"environment_id"`
	PromotionID       string                 `json:"promotion_id,omitempty"`
	ApprovalRequired  bool                   `json:"approval_required"`
	ApprovalExpiresAt *time.Time             `json:"approval_expires_at,omitempty"`
	RecordedAt        time.Time              `json:"recorded_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	PlanExpiresAt     time.Time              `json:"plan_expires_at"`
	RetainedUntil     time.Time              `json:"retained_until"`
	Apply             environmentActionPhase `json:"apply"`
}

type environmentActionSnapshot struct {
	Version int                                `json:"version"`
	Items   map[string]environmentActionRecord `json:"items"`
}

func (m *management) restoreEnvironmentActionHistory() error {
	var saved environmentActionSnapshot
	if err := m.opts.planStore.read(environmentActionsNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid environment action history format or capacity")
	}
	for id, entry := range saved.Items {
		if id != entry.PlanID || validateEnvironmentAction(entry) != nil || entry.APIBase != m.opts.APIBase || m.opts.accountID != "" && entry.AccountID != m.opts.accountID {
			return errors.New("invalid environment action history binding")
		}
		if _, duplicate := m.actions[id]; duplicate {
			return errors.New("duplicate environment action history identity")
		}
	}
	m.environmentActions = saved.Items
	if m.environmentActions == nil {
		m.environmentActions = make(map[string]environmentActionRecord)
	}
	return nil
}

func (m *management) syncEnvironmentActionHistory(now time.Time) error {
	if m.environmentActions == nil {
		m.environmentActions = make(map[string]environmentActionRecord)
	}
	for _, p := range m.environmentPlans {
		if !now.Before(p.ExpiresAt.Add(api.ManagementMCPActionHistoryRetention)) {
			continue
		}
		entry := recordEnvironmentAction(p)
		entry.RecordedAt, entry.UpdatedAt = now, now
		if previous, ok := m.environmentActions[p.ID]; ok {
			entry.RecordedAt, entry.UpdatedAt = previous.RecordedAt, previous.UpdatedAt
			if !reflect.DeepEqual(entry, previous) {
				entry.UpdatedAt = now
			}
		}
		if validateEnvironmentAction(entry) != nil {
			return invalid("invalid_response", "environment history could not bind the reviewed plan and receipt")
		}
		if _, duplicate := m.actions[p.ID]; duplicate {
			return invalid("invalid_response", "duplicate environment action history identity")
		}
		m.environmentActions[p.ID] = entry
	}
	for id, entry := range m.environmentActions {
		if !now.Before(entry.RetainedUntil) {
			delete(m.environmentActions, id)
		}
	}
	return nil
}

func recordEnvironmentAction(p *environmentPlan) environmentActionRecord {
	e := environmentActionRecord{PlanID: p.ID, Action: p.Action, AccountID: p.AccountID, APIBase: p.APIBase, Project: p.Project, ProjectID: p.ProjectID, FromEnvironment: p.FromEnvironment, FromID: p.FromID, Environment: p.Environment, EnvironmentID: p.EnvironmentID, PromotionID: p.PromotionID, ApprovalRequired: p.ApprovalRequired || p.approvalRequest != nil, PlanExpiresAt: p.ExpiresAt, RetainedUntil: p.ExpiresAt.Add(api.ManagementMCPActionHistoryRetention), Apply: environmentActionPhase{State: "not_attempted"}}
	if p.attempted {
		e.Apply.State = "outcome_unknown"
	} else if p.approvalRequest != nil && p.approval == "" {
		e.Apply.State = "approval_pending"
		if expiry, err := time.Parse(time.RFC3339Nano, p.approvalRequest.ExpiresAt); err == nil {
			e.ApprovalExpiresAt = &expiry
		}
	}
	if p.attempted && p.result != nil {
		// Restored results are RawMessage. Project only checked identity fields;
		// native statuses, errors, workloads and approval material stay private.
		var result struct {
			PromotionID string `json:"promotion_id"`
			Project     string `json:"project_slug"`
			From        string `json:"from_environment"`
			To          string `json:"to_environment"`
		}
		data, err := json.Marshal(p.result)
		if err == nil && json.Unmarshal(data, &result) == nil && validateSegment(result.PromotionID, "promotion_id") == nil && result.Project == p.Project && result.From == p.FromEnvironment && result.To == p.Environment && (p.Action != "environment_rollback" || result.PromotionID == p.PromotionID) {
			e.Apply = environmentActionPhase{State: "accepted", Receipt: &environmentActionReceipt{PromotionID: result.PromotionID}}
		}
	}
	return e
}

func validateEnvironmentAction(e environmentActionRecord) error {
	if !validActionID(e.PlanID) || e.Action != "environment_promotion" && e.Action != "environment_rollback" || validateSegment(e.AccountID, "account_id") != nil || validateSegment(e.Project, "project") != nil || validateSegment(e.ProjectID, "project_id") != nil || validateSegment(e.FromID, "from_environment_id") != nil || validateSegment(e.EnvironmentID, "environment_id") != nil || !api.ValidProjectEnvironmentSlug(e.FromEnvironment) || !api.ValidProjectEnvironmentSlug(e.Environment) || e.FromEnvironment == e.Environment || e.FromID == e.EnvironmentID {
		return errors.New("invalid environment action identity")
	}
	if e.RecordedAt.IsZero() || e.UpdatedAt.Before(e.RecordedAt) || e.PlanExpiresAt.IsZero() || !e.RetainedUntil.Equal(e.PlanExpiresAt.Add(api.ManagementMCPActionHistoryRetention)) || !e.RecordedAt.Before(e.RetainedUntil) || !e.UpdatedAt.Before(e.RetainedUntil) {
		return errors.New("invalid environment action retention")
	}
	if e.Action == "environment_rollback" && (validateSegment(e.PromotionID, "promotion_id") != nil || e.ApprovalRequired) || e.Action == "environment_promotion" && e.PromotionID != "" {
		return errors.New("invalid environment action promotion")
	}
	if e.ApprovalExpiresAt != nil && (e.Apply.State != "approval_pending" || e.ApprovalExpiresAt.IsZero()) {
		return errors.New("unexpected environment action approval expiry")
	}
	switch e.Apply.State {
	case "not_attempted", "outcome_unknown", "approval_pending":
		if e.Apply.Receipt != nil || e.Apply.State == "approval_pending" && (e.Action != "environment_promotion" || !e.ApprovalRequired) {
			return errors.New("invalid environment action phase")
		}
	case "accepted":
		if e.Apply.Receipt == nil || validateSegment(e.Apply.Receipt.PromotionID, "promotion_id") != nil || e.Action == "environment_rollback" && e.Apply.Receipt.PromotionID != e.PromotionID {
			return errors.New("invalid environment action receipt")
		}
	default:
		return errors.New("invalid environment action phase")
	}
	return nil
}

func (m *management) environmentActionPlanAvailable(e environmentActionRecord, now time.Time) bool {
	p := m.environmentPlans[e.PlanID]
	return p != nil && p.Action == e.Action && p.AccountID == e.AccountID && p.APIBase == e.APIBase && p.ProjectID == e.ProjectID && p.EnvironmentID == e.EnvironmentID && p.FromID == e.FromID && now.Before(p.ExpiresAt)
}
