package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type listActionsArgs struct {
	App         string `json:"app" jsonschema:"Existing Gregale app slug"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 50; default 20"`
	Before      string `json:"before,omitempty" jsonschema:"Opaque next_before cursor from the same connection and app/environment"`
}

type getActionArgs struct {
	App         string `json:"app" jsonschema:"Existing Gregale app slug"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
	PlanID      string `json:"plan_id" jsonschema:"Exact plan_id from this connection's action history"`
}

type actionView struct {
	Review   *savedPlanReference  `json:"review,omitempty"`
	Recovery *actionRecoveryLinks `json:"recovery,omitempty"`
	actionRecord
	PlanAvailable bool               `json:"plan_available"`
	NextStep      string             `json:"next_step"`
	RetryTool     string             `json:"retry_tool,omitempty"`
	Observation   *actionObservation `json:"observation,omitempty"`
}

type actionHistoryMetadata struct {
	ObservedAt       time.Time `json:"observed_at"`
	Durable          bool      `json:"durable"`
	Coverage         string    `json:"coverage"`
	SupportedActions []string  `json:"supported_actions"`
	RetentionSeconds int64     `json:"retention_seconds_after_plan_expiry"`
	Capacity         int       `json:"capacity"`
	CapacityScope    string    `json:"capacity_scope"`
	NativeCompletion string    `json:"native_completion"`
}

type actionHistoryPage struct {
	actionHistoryMetadata
	Items      []actionView `json:"items"`
	NextBefore string       `json:"next_before,omitempty"`
}

type actionHistoryDetail struct {
	actionHistoryMetadata
	Action actionView `json:"action"`
}

type actionCursor struct {
	Version int       `json:"v"`
	Binding string    `json:"binding"`
	At      time.Time `json:"at"`
	ID      string    `json:"id"`
}

func registerActionHistory(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_actions", "List this connection's bounded, redacted deploy, rollback, service/worker rollout, service abort, route-budget and regression-triage action history for one exact app/environment. Historical receipts do not prove deployment completion or grant retry authority. Regression records require current diagnostic consent.", m.listActions)
	register(s, m, "gregale_get_action", "Inspect one connection-local plan's recorded attempts and receipts, including after plan expiry. Lost responses remain outcome_unknown in recorded history; eligible attempts expose independent native acceptance lookups. Never substitute a new plan for an ambiguous expired attempt.", m.getAction)
}

func (m *management) actionTarget(ctx context.Context, args appArgs) (actionRecord, error) {
	var target actionRecord
	if !m.opts.planStore.available() {
		return target, invalid("state_unavailable", "action history storage is unavailable; restore storage and restart")
	}
	if err := m.authorizeArgs(ctx, args); err != nil {
		return target, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return target, err
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return target, err
	}
	if validateSegment(account.ID, "account_id") != nil || app.Slug != args.App || validateSegment(app.ID, "app_id") != nil || !m.appIDAllowed(app.ID) || m.opts.accountID != "" && account.ID != m.opts.accountID {
		return target, invalid("app_access_denied", "account or app identity is outside the connection")
	}
	return actionRecord{AccountID: account.ID, APIBase: m.opts.APIBase, App: args.App, AppID: app.ID, Scope: normalizedScope(args.Environment), Project: args.Project, ProjectID: env.ProjectID, EnvironmentID: env.ID}, nil
}

func actionTargetMatches(entry, target actionRecord) bool {
	return entry.AccountID == target.AccountID && entry.APIBase == target.APIBase && entry.App == target.App && entry.AppID == target.AppID && entry.Scope == target.Scope && entry.Project == target.Project && entry.ProjectID == target.ProjectID && entry.EnvironmentID == target.EnvironmentID
}

func (m *management) guardActionHistory(value any, args appArgs, target actionRecord, diagnostics bool) *guardedRead {
	read := &guardedRead{value: value}
	read.check = func(ctx context.Context) error {
		current, err := m.actionTarget(ctx, args)
		if err != nil {
			return err
		}
		if !actionTargetMatches(current, target) || diagnostics && !m.requestReadAllowed(target.App, target.AppID) {
			return invalid("app_access_denied", "action history identity or diagnostic consent changed during the read")
		}
		return m.refreshHistoryRecoveryLinks(read)
	}
	return read
}

func (m *management) actionHistoryMetadata(now time.Time) actionHistoryMetadata {
	return actionHistoryMetadata{ObservedAt: now, Durable: m.opts.planStore != nil, Coverage: "bounded_connection_history_not_account_audit", SupportedActions: []string{"deploy", "rollback", "regression_action", "workload_rollout", "service_abort", "route_budgets"}, RetentionSeconds: int64(api.ManagementMCPActionHistoryRetention / time.Second), Capacity: api.ManagementMCPActionHistoryMax, CapacityScope: "all_connection_action_history", NativeCompletion: "not_observed"}
}

func (m *management) listActions(ctx context.Context, args listActionsArgs) (any, error) {
	limit := args.Limit
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit < 1 || limit > api.ManagementMCPActionHistoryPageMax {
		return nil, invalid("invalid_argument", "limit must be from 1 to 50")
	}
	selector := appArgs{App: args.App, Project: args.Project, Environment: args.Environment}
	target, err := m.actionTarget(ctx, selector)
	if err != nil {
		return nil, err
	}
	binding := m.actionCursorBinding(target)
	before, err := decodeActionCursor(args.Before, binding)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	m.mu.Lock()
	entries := make([]actionRecord, 0, len(m.actions))
	for _, entry := range m.actions {
		if !now.Before(entry.RetainedUntil) || !actionTargetMatches(entry, target) || entry.Action == "regression_action" && !m.requestReadAllowed(entry.App, entry.AppID) {
			continue
		}
		if before != nil && !actionNewer(actionRecord{RecordedAt: before.At, PlanID: before.ID}, entry) {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return actionNewer(entries[i], entries[j]) })
	page := actionHistoryPage{actionHistoryMetadata: m.actionHistoryMetadata(now), Items: make([]actionView, 0, min(limit, len(entries)))}
	diagnostics := false
	for _, entry := range entries[:min(limit, len(entries))] {
		page.Items = append(page.Items, m.actionView(entry, now))
		diagnostics = diagnostics || entry.Action == "regression_action"
	}
	if len(entries) > limit {
		last := entries[limit-1]
		encoded, _ := json.Marshal(actionCursor{Version: 1, Binding: binding, At: last.RecordedAt, ID: last.PlanID})
		page.NextBefore = base64.RawURLEncoding.EncodeToString(encoded)
	}
	m.mu.Unlock()
	return m.guardActionHistory(page, selector, target, diagnostics), nil
}

func (m *management) getAction(ctx context.Context, args getActionArgs) (any, error) {
	if !validActionID(args.PlanID) {
		return nil, invalid("invalid_argument", "plan_id must be an exact action history plan ID")
	}
	selector := appArgs{App: args.App, Project: args.Project, Environment: args.Environment}
	target, err := m.actionTarget(ctx, selector)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	m.mu.Lock()
	entry, ok := m.actions[args.PlanID]
	view := m.actionView(entry, now)
	m.mu.Unlock()
	if !ok || !now.Before(entry.RetainedUntil) || !actionTargetMatches(entry, target) {
		return nil, invalid("action_not_found", "action is not retained for this connection and app/environment")
	}
	diagnostics := entry.Action == "regression_action"
	if diagnostics && !m.requestReadAllowed(entry.App, entry.AppID) {
		return nil, invalid("request_access_denied", "regression action history requires current diagnostic consent")
	}
	return m.guardActionHistory(actionHistoryDetail{actionHistoryMetadata: m.actionHistoryMetadata(now), Action: view}, selector, target, diagnostics), nil
}

// This is recovery guidance, never a substitute for checkedPlan authorization,
// original plan review, native guards, plan expiry, or completion observation.
func (m *management) actionView(entry actionRecord, now time.Time) actionView {
	view := actionView{actionRecord: entry, PlanAvailable: m.actionPlanAvailable(entry, now), NextStep: "inspect_recorded_receipt"}
	if view.PlanAvailable {
		environment := entry.Scope
		if environment == api.DefaultEnvScope {
			environment = ""
		}
		view.Review = &savedPlanReference{Tool: "gregale_get_plan", Arguments: getActionArgs{App: entry.App, Project: entry.Project, Environment: environment, PlanID: entry.PlanID}}
		view.Recovery = m.recoveryLinks(getActionArgs{App: entry.App, Project: entry.Project, Environment: environment, PlanID: entry.PlanID}, false)
	}
	phase, tool := entry.Apply, map[string]string{"deploy": "gregale_apply_deploy", "rollback": "gregale_apply_rollback", "regression_action": "gregale_apply_regression_action", "workload_rollout": "gregale_apply_workload_rollout", "service_abort": "gregale_apply_service_abort", "route_budgets": "gregale_apply_route_budgets"}[entry.Action]
	view.Observation = workloadActionObservation(entry)
	if entry.Action == "route_budgets" {
		view.Observation = budgetActionObservation(entry)
		if phase.State == "committed" {
			view.NextStep = "inspect_current_route_budgets"
		}
	}
	if phase.State == "accepted" && view.Observation != nil {
		view.NextStep = "observe_native_rollout"
	}
	if entry.Promotion != nil && entry.Promotion.State == "outcome_unknown" {
		phase, tool = *entry.Promotion, "gregale_promote_deployment"
	}
	switch phase.State {
	case "not_attempted":
		view.NextStep = "review_original_plan_before_apply"
		if !view.PlanAvailable {
			view.NextStep = "inspect_native_state_before_replanning"
		}
	case "outcome_unknown":
		view.NextStep = "inspect_native_state_before_replanning"
		if view.PlanAvailable && m.opts.AllowWrites {
			view.NextStep, view.RetryTool = "retry_same_plan_with_current_authorization", tool
		}
	}
	return m.addOutcomeRecoveryLinks(view, now)
}

func (m *management) actionCursorBinding(target actionRecord) string {
	data, _ := json.Marshal([]string{m.opts.planNamespace, target.APIBase, target.AccountID, target.AppID, target.Scope, target.ProjectID, target.EnvironmentID})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func decodeActionCursor(raw, binding string) (*actionCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > api.ManagementMCPActionCursorMaxBytes {
		return nil, invalid("invalid_argument", "action history cursor is too long")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalid("invalid_argument", "invalid action history cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cursor actionCursor
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Binding != binding || cursor.At.IsZero() || !validActionID(cursor.ID) {
		return nil, invalid("invalid_argument", "action history cursor belongs to another connection or target, or is invalid")
	}
	return &cursor, nil
}
