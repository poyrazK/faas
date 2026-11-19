package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Edge rules (ADR-20261118140000). Reads show an app's rules, attack mode,
// hit counts and recent rule events; end-user identifiers in events (client
// IP, user agent) need data:read consent. Changes are reviewed operation
// plans pinned to the app's current rule-set version: apply refuses when the
// rules changed after the plan. apid asks a person before any production
// edge change, as it does for every agent credential.

// endUserKeys hold end-user identifiers in edge rule events.
var endUserKeys = []string{"client_ip", "user_agent"}

type edgeActivityArgs struct {
	App     string `json:"app" jsonschema:"App slug"`
	Window  string `json:"window,omitempty" jsonschema:"Hit-count window: 1h, 24h or 7d; default 24h"`
	RuleID  string `json:"rule_id,omitempty" jsonschema:"Only this rule's events"`
	Outcome string `json:"outcome,omitempty" jsonschema:"Only events with this outcome, such as blocked, challenged or logged"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Events: 1 to 100; default 20"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page"`
}

type planEdgeArgs struct {
	App             string         `json:"app" jsonschema:"App slug"`
	Op              string         `json:"op" jsonschema:"create, update, delete, rollback, attack_mode_on or attack_mode_off"`
	RuleID          string         `json:"rule_id,omitempty" jsonschema:"update, delete: the rule"`
	Create          map[string]any `json:"create,omitempty" jsonschema:"create: the rule as POST /v1/apps/{slug}/edge-rules takes it: kind, action, match_host, match_path, match_methods, priority, mode, expires_at, ..."`
	Update          map[string]any `json:"update,omitempty" jsonschema:"update: only the fields to change, as PATCH /v1/edge-rules/{id} takes them"`
	Version         int            `json:"version,omitempty" jsonschema:"rollback: rule-set version from gregale_list_edge_rules"`
	DurationSeconds int            `json:"duration_seconds,omitempty" jsonschema:"attack_mode_on: how long it stays on; it always ends on its own"`
	Reason          string         `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type edgeChangeRequest struct {
	Op              string                     `json:"op"`
	RuleID          string                     `json:"rule_id,omitempty"`
	Create          *api.CreateEdgeRuleRequest `json:"create,omitempty"`
	Update          *api.UpdateEdgeRuleRequest `json:"update,omitempty"`
	Version         int                        `json:"version,omitempty"`
	DurationSeconds int                        `json:"duration_seconds,omitempty"`
	// RulesSHA256 is the rule set the plan was made against.
	RulesSHA256 string `json:"rules_sha256"`
}

func registerEdgeRules(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_edge_rules", "List the app's edge rules (blocks, rate limits, challenges, redirects, rewrites, WAF), whether attack mode is on, and the rule-set versions to roll back to. Read-only.", m.listEdgeRules)
	register(s, m, "gregale_get_edge_activity", "Read what the app's edge rules did: hits per rule over a window and recent rule events (blocked, challenged or logged requests). Client IPs and user agents need data:read consent. Read-only.", m.getEdgeActivity)
	register(s, m, "gregale_plan_edge_rule_change", "Review an edge change: create, update or delete a rule, roll the rules back to a version, or switch attack mode on or off. The plan is pinned to the current rules. Changes nothing; apply with gregale_apply_operation. On a production app an agent connection asks a person first.", m.planEdgeRuleChange)
}

func (m *management) listEdgeRules(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	rules, err := m.client.ListEdgeRulesForApp(ctx, a.App)
	if err != nil {
		return nil, err
	}
	attack, err := m.client.GetAppEdgeAttackMode(ctx, a.App)
	if err != nil {
		return nil, err
	}
	versions, err := m.client.ListEdgeRuleSetVersions(ctx, a.App)
	if err != nil {
		return nil, err
	}
	for i := range versions {
		versions[i].Rules = nil
	}
	return map[string]any{"rules": rules, "attack_mode": attack, "versions": versions}, nil
}

func (m *management) getEdgeActivity(ctx context.Context, a edgeActivityArgs) (any, error) {
	limit, err := pageSize(a.Limit)
	if err != nil {
		return nil, err
	}
	window := a.Window
	if window == "" {
		window = "24h"
	}
	if window != "1h" && window != "24h" && window != "7d" {
		return nil, invalid("invalid_argument", "window must be 1h, 24h or 7d")
	}
	for field, v := range map[string]string{"rule_id": a.RuleID, "outcome": a.Outcome, "cursor": a.Cursor} {
		if v != "" {
			if err := validateSegment(v, field); err != nil {
				return nil, err
			}
		}
	}
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	stats, err := m.client.GetEdgeRuleStats(ctx, a.App, window)
	if err != nil {
		return nil, err
	}
	events, err := m.client.ListEdgeRuleEvents(ctx, a.App, api.EdgeRuleEventsQuery{RuleID: a.RuleID, Outcome: a.Outcome, Limit: limit, Cursor: a.Cursor})
	if err != nil {
		return nil, err
	}
	return m.redactKeys(map[string]any{"stats": stats, "events": events}, endUserKeys)
}

// currentRuleSet returns the app's current rule-set version.
func (m *management) currentRuleSet(ctx context.Context, app string) (api.EdgeRuleSetVersionResponse, error) {
	versions, err := m.client.ListEdgeRuleSetVersions(ctx, app)
	if err != nil {
		return api.EdgeRuleSetVersionResponse{}, err
	}
	for _, v := range versions {
		if v.Current {
			return v, nil
		}
	}
	return api.EdgeRuleSetVersionResponse{}, nil
}

// editableRule returns a rule of the app that people own: attack mode and
// other managed rules change only through their own switch.
func (m *management) editableRule(ctx context.Context, appID, id string) (api.EdgeRuleResponse, error) {
	if err := validateSegment(id, "rule_id"); err != nil {
		return api.EdgeRuleResponse{}, err
	}
	rule, err := m.client.GetEdgeRule(ctx, id)
	if err != nil {
		return rule, err
	}
	if rule.ID != id || rule.AppID != appID {
		return rule, invalid("app_access_denied", "the edge rule belongs to a different app or to the account")
	}
	if rule.ManagedBy != "" {
		return rule, invalid("invalid_argument", "the rule is managed by "+rule.ManagedBy+"; change it through that switch")
	}
	return rule, nil
}

func (m *management) planEdgeRuleChange(ctx context.Context, a planEdgeArgs) (any, error) {
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	req := edgeChangeRequest{Op: a.Op}
	view := map[string]any{}
	effect := ""
	switch a.Op {
	case "create":
		var create api.CreateEdgeRuleRequest
		if err := strictDecode(a.Create, &create, "create"); err != nil {
			return nil, err
		}
		if create.Kind == "" || len(create.Action) == 0 {
			return nil, invalid("invalid_argument", "create needs a rule with kind and action")
		}
		req.Create = &create
		view["rule"] = create
		effect = "Adds an edge rule; matching requests are handled by it from the next request on."
	case "update", "delete":
		rule, err := m.editableRule(ctx, app.ID, a.RuleID)
		if err != nil {
			return nil, err
		}
		req.RuleID = a.RuleID
		view["before"] = rule
		if a.Op == "update" {
			if len(a.Update) == 0 {
				return nil, invalid("invalid_argument", "update needs the fields to change")
			}
			var update api.UpdateEdgeRuleRequest
			if err := strictDecode(a.Update, &update, "update"); err != nil {
				return nil, err
			}
			req.Update = &update
			view["changes"] = update
			effect = "Changes how the rule handles matching requests from the next request on."
		} else {
			effect = "Removes the rule; requests it handled reach the app or the next rule."
		}
	case "rollback":
		if a.Version < 1 {
			return nil, invalid("invalid_argument", "rollback needs version")
		}
		target, err := m.client.GetEdgeRuleSetVersion(ctx, a.App, a.Version)
		if err != nil {
			return nil, err
		}
		if target.Current {
			return nil, invalid("invalid_argument", "version "+strconv.Itoa(a.Version)+" is already current")
		}
		req.Version = a.Version
		view["target"] = target
		effect = "Replaces every app rule with the rules of that version."
	case "attack_mode_on":
		probe := api.EnableEdgeAttackModeRequest{DurationSeconds: a.DurationSeconds}
		if p := probe.Validate(); p != nil {
			return nil, invalid("invalid_argument", p.Detail)
		}
		req.DurationSeconds = probe.DurationSeconds
		effect = fmt.Sprintf("Challenges every request for %d seconds: browsers solve it once; verified crawlers and authenticated API calls pass; other clients get 403.", probe.DurationSeconds)
	case "attack_mode_off":
		effect = "Stops challenging every request; the app's other rules still apply."
	default:
		return nil, invalid("invalid_argument", "op must be create, update, delete, rollback, attack_mode_on or attack_mode_off")
	}
	if (a.Op == "update" || a.Op == "delete") != (a.RuleID != "") {
		return nil, invalid("invalid_argument", "rule_id is required for update and delete, and refused otherwise")
	}
	current, err := m.currentRuleSet(ctx, a.App)
	if err != nil {
		return nil, err
	}
	req.RulesSHA256 = current.RulesSHA256
	view["request"] = req
	view["current_version"] = current.Version
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "edge_change", req)
	if err != nil {
		return nil, err
	}
	p.Operation.Changes, _ = json.Marshal(view)
	p.TrafficEffect = effect
	return m.savePlan(p)
}

// strictDecode reads a free-form tool argument into a native request,
// refusing fields the request does not have.
func strictDecode(v map[string]any, out any, field string) error {
	if v == nil {
		return invalid("invalid_argument", field+" is required")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return invalid("invalid_argument", field+" is not JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return invalid("invalid_argument", field+": "+err.Error())
	}
	return nil
}

// runEdgeChange applies an edge plan. The rule-set pin is checked only on
// the first attempt: a retry after an uncertain response may find this plan's
// own change already in place, and its idempotency key replays that outcome.
func (m *management) runEdgeChange(ctx context.Context, p *releasePlan, retry bool) (any, error) {
	var req edgeChangeRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	if !retry {
		current, err := m.currentRuleSet(ctx, p.App)
		if err != nil {
			return nil, err
		}
		if current.RulesSHA256 != req.RulesSHA256 {
			return nil, invalid("plan_stale", "the app's edge rules changed after this plan; plan again")
		}
	}
	ctx = api.ContextWithIdempotencyKey(ctx, "mcp-operation-"+p.ID)
	switch req.Op {
	case "create":
		return m.client.CreateEdgeRule(ctx, p.App, *req.Create)
	case "update":
		return m.client.UpdateEdgeRule(ctx, req.RuleID, *req.Update)
	case "delete":
		if err := m.client.DeleteEdgeRule(ctx, req.RuleID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": req.RuleID}, nil
	case "rollback":
		return m.client.RollbackEdgeRules(ctx, p.App, req.Version)
	case "attack_mode_on":
		return m.client.EnableAppEdgeAttackMode(ctx, p.App, req.DurationSeconds)
	case "attack_mode_off":
		return m.client.DisableAppEdgeAttackMode(ctx, p.App)
	}
	return nil, invalid("invalid_plan", "unsupported edge change")
}
