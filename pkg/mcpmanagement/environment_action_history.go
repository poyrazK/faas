package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type listEnvironmentActionsArgs struct {
	Project     string `json:"project" jsonschema:"Existing Gregale project slug"`
	Environment string `json:"environment" jsonschema:"Exact target named environment"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 50; default 20"`
	Before      string `json:"before,omitempty" jsonschema:"Opaque next_before cursor for this connection and target"`
}

type getEnvironmentActionArgs struct {
	Project     string `json:"project" jsonschema:"Existing Gregale project slug"`
	Environment string `json:"environment" jsonschema:"Exact target named environment"`
	PlanID      string `json:"plan_id" jsonschema:"Exact connection-local environment action plan ID"`
}

type environmentActionObservation struct {
	Tool      string                   `json:"tool"`
	Arguments environmentPromotionArgs `json:"arguments"`
}

type environmentActionView struct {
	environmentActionRecord
	PlanAvailable bool                          `json:"plan_available"`
	NextStep      string                        `json:"next_step"`
	RetryTool     string                        `json:"retry_tool,omitempty"`
	Observation   *environmentActionObservation `json:"observation,omitempty"`
}

type environmentActionPage struct {
	actionHistoryMetadata
	Items      []environmentActionView `json:"items"`
	NextBefore string                  `json:"next_before,omitempty"`
}

type environmentActionDetail struct {
	actionHistoryMetadata
	Action environmentActionView `json:"action"`
}

func registerEnvironmentActionHistory(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_environment_actions", "List this connection's bounded, redacted promotion/rollback history for an exact project and target environment. Both environments and all project workloads require current read consent. Approval states are recorded snapshots, never approval authority or native completion.", m.listEnvironmentActions)
	register(s, m, "gregale_get_environment_action", "Inspect an original environment plan's recorded approval/attempt and promotion reference, including after expiry. Approval credentials, review links and workload payloads are excluded. Resume only through the original guarded apply tool; human approval remains mandatory.", m.getEnvironmentAction)
}

func (m *management) environmentHistoryTarget(ctx context.Context, args environmentArgs) (environmentActionRecord, error) {
	var target environmentActionRecord
	if !m.opts.planStore.available() {
		return target, invalid("state_unavailable", "action history storage is unavailable; restore storage and restart")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return target, err
	}
	p, e, err := m.environmentAccess(ctx, args.Project, args.Environment, false, true)
	if err != nil {
		return target, err
	}
	if validateSegment(account.ID, "account_id") != nil || validateSegment(p.ID, "project_id") != nil || validateSegment(e.ID, "environment_id") != nil || m.opts.accountID != "" && m.opts.accountID != account.ID {
		return target, invalid("environment_access_denied", "account or environment identity is outside this connection")
	}
	return environmentActionRecord{AccountID: account.ID, APIBase: m.opts.APIBase, Project: args.Project, ProjectID: p.ID, Environment: args.Environment, EnvironmentID: e.ID}, nil
}

func environmentHistoryTargetMatches(e, target environmentActionRecord) bool {
	return e.AccountID == target.AccountID && e.APIBase == target.APIBase && e.Project == target.Project && e.ProjectID == target.ProjectID && e.Environment == target.Environment && e.EnvironmentID == target.EnvironmentID
}

func (m *management) environmentHistorySource(ctx context.Context, entry environmentActionRecord) (bool, error) {
	if m.opts.Environments != nil {
		if _, ok := m.opts.Environments.Named[entry.Project+"/"+entry.FromEnvironment]; !ok {
			return false, nil
		}
	}
	p, source, err := m.environmentAccess(ctx, entry.Project, entry.FromEnvironment, false, true)
	return err == nil && p.ID == entry.ProjectID && source.ID == entry.FromID, err
}

func (m *management) guardEnvironmentHistory(value any, target environmentActionRecord, sources []environmentActionRecord) *guardedRead {
	return &guardedRead{value: value, check: func(ctx context.Context) error {
		for _, source := range sources {
			allowed, err := m.environmentHistorySource(ctx, source)
			if err != nil {
				return err
			}
			if !allowed {
				return invalid("environment_access_denied", "environment history source identity or consent changed during the read")
			}
		}
		// As with native promotion inspection, finish by checking the target
		// after source lookups, which can observe a concurrent target change.
		current, err := m.environmentHistoryTarget(ctx, environmentArgs{Project: target.Project, Environment: target.Environment})
		if err != nil {
			return err
		}
		if !environmentHistoryTargetMatches(current, target) {
			return invalid("environment_access_denied", "environment history identity changed during the read")
		}
		return nil
	}}
}

func (m *management) environmentHistoryMetadata(now time.Time) actionHistoryMetadata {
	metadata := m.actionHistoryMetadata(now)
	metadata.SupportedActions = []string{"environment_promotion", "environment_rollback"}
	return metadata
}

func (m *management) listEnvironmentActions(ctx context.Context, args listEnvironmentActionsArgs) (any, error) {
	limit := args.Limit
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit < 1 || limit > api.ManagementMCPActionHistoryPageMax {
		return nil, invalid("invalid_argument", "limit must be from 1 to 50")
	}
	target, err := m.environmentHistoryTarget(ctx, environmentArgs{Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal([]string{"environment-action-history", m.opts.planNamespace, target.APIBase, target.AccountID, target.ProjectID, target.EnvironmentID})
	digest := sha256.Sum256(data)
	binding := hex.EncodeToString(digest[:])
	before, err := decodeActionCursor(args.Before, binding)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rows := m.environmentHistoryRows(target, before, now)
	return m.environmentHistoryPage(ctx, rows, target, limit, binding, now)
}

func (m *management) environmentHistoryRows(target environmentActionRecord, before *actionCursor, now time.Time) []environmentActionView {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]environmentActionView, 0, len(m.environmentActions))
	for _, entry := range m.environmentActions {
		position := actionRecord{RecordedAt: entry.RecordedAt, PlanID: entry.PlanID}
		if now.Before(entry.RetainedUntil) && environmentHistoryTargetMatches(entry, target) && (before == nil || actionNewer(actionRecord{RecordedAt: before.At, PlanID: before.ID}, position)) {
			rows = append(rows, m.environmentActionView(entry, now))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return actionNewer(actionRecord{RecordedAt: rows[i].RecordedAt, PlanID: rows[i].PlanID}, actionRecord{RecordedAt: rows[j].RecordedAt, PlanID: rows[j].PlanID})
	})
	return rows
}

func (m *management) environmentHistoryPage(ctx context.Context, rows []environmentActionView, target environmentActionRecord, limit int, binding string, now time.Time) (any, error) {
	page := environmentActionPage{actionHistoryMetadata: m.environmentHistoryMetadata(now), Items: []environmentActionView{}}
	sources := map[string]environmentActionRecord{}
	allowedSources := map[string]bool{}
	for _, row := range rows {
		key := row.FromEnvironment + "/" + row.FromID
		if _, checked := allowedSources[key]; !checked {
			allowed, err := m.environmentHistorySource(ctx, row.environmentActionRecord)
			if err != nil {
				return nil, err
			}
			allowedSources[key] = allowed
			if allowed {
				sources[key] = row.environmentActionRecord
			}
		}
		if !allowedSources[key] {
			continue
		}
		page.Items = append(page.Items, row)
		if len(page.Items) > limit {
			last := page.Items[limit-1]
			encoded, _ := json.Marshal(actionCursor{Version: 1, Binding: binding, At: last.RecordedAt, ID: last.PlanID})
			page.NextBefore = base64.RawURLEncoding.EncodeToString(encoded)
			page.Items = page.Items[:limit]
			break
		}
	}
	checkedSources := make([]environmentActionRecord, 0, len(sources))
	for _, source := range sources {
		checkedSources = append(checkedSources, source)
	}
	return m.guardEnvironmentHistory(page, target, checkedSources), nil
}

func (m *management) getEnvironmentAction(ctx context.Context, args getEnvironmentActionArgs) (any, error) {
	if !validActionID(args.PlanID) {
		return nil, invalid("invalid_argument", "plan_id must be an exact environment action plan ID")
	}
	target, err := m.environmentHistoryTarget(ctx, environmentArgs{Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	m.mu.Lock()
	entry, ok := m.environmentActions[args.PlanID]
	view := m.environmentActionView(entry, now)
	m.mu.Unlock()
	if !ok || !now.Before(entry.RetainedUntil) || !environmentHistoryTargetMatches(entry, target) {
		return nil, invalid("action_not_found", "environment action is not retained for this connection and target")
	}
	allowed, err := m.environmentHistorySource(ctx, entry)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, invalid("action_not_found", "environment action is not retained for this connection and source")
	}
	return m.guardEnvironmentHistory(environmentActionDetail{actionHistoryMetadata: m.environmentHistoryMetadata(now), Action: view}, target, []environmentActionRecord{entry}), nil
}

func (m *management) environmentActionView(e environmentActionRecord, now time.Time) environmentActionView {
	view := environmentActionView{environmentActionRecord: e, PlanAvailable: m.environmentActionPlanAvailable(e, now), NextStep: "inspect_recorded_receipt"}
	id := e.PromotionID
	if e.Apply.Receipt != nil {
		id = e.Apply.Receipt.PromotionID
	}
	if id != "" {
		view.Observation = &environmentActionObservation{Tool: "gregale_get_environment_promotion", Arguments: environmentPromotionArgs{Project: e.Project, Environment: e.Environment, PromotionID: id}}
	}
	switch e.Apply.State {
	case "not_attempted":
		view.NextStep = "review_original_plan_before_apply"
	case "approval_pending":
		view.NextStep = "inspect_recorded_approval_state"
		if view.PlanAvailable && m.opts.AllowWrites && (e.ApprovalExpiresAt == nil || now.Before(*e.ApprovalExpiresAt)) {
			view.NextStep, view.RetryTool = "resume_original_human_approval_flow", "gregale_apply_environment_promotion"
		}
	case "outcome_unknown":
		view.NextStep = "inspect_native_state_before_replanning"
		if view.PlanAvailable && m.opts.AllowWrites {
			view.NextStep = "retry_same_plan_with_current_authorization"
			view.RetryTool = map[string]string{"environment_promotion": "gregale_apply_environment_promotion", "environment_rollback": "gregale_apply_environment_rollback"}[e.Action]
		}
	case "accepted":
		view.NextStep = "observe_native_promotion"
	}
	if !view.PlanAvailable && e.Apply.State != "accepted" {
		view.NextStep, view.RetryTool = "inspect_native_state_before_replanning", ""
	}
	return view
}
