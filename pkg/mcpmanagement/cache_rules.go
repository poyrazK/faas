package mcpmanagement

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const cacheRulePolicyScope = "app_shared"

type cacheRulePlanArgs struct {
	App         string                  `json:"app" jsonschema:"Gregale app slug"`
	Project     string                  `json:"project,omitempty" jsonschema:"Named environment changes are currently unsupported"`
	Environment string                  `json:"environment,omitempty" jsonschema:"Must be omitted: this operation changes shared app policy"`
	RuleID      string                  `json:"rule_id" jsonschema:"Existing unconditional enabled app-wide cache rule ID"`
	Reason      string                  `json:"reason" jsonschema:"Reviewable reason, at most 280 characters"`
	Cache       api.EdgeRuleCacheAction `json:"cache" jsonschema:"Complete proposed cache settings; native defaults appear in the review"`
}

type cacheRuleView struct {
	ID       string                  `json:"id"`
	Host     string                  `json:"host"`
	Path     string                  `json:"path"`
	Methods  []string                `json:"methods"`
	Priority int                     `json:"priority"`
	Cache    api.EdgeRuleCacheAction `json:"cache"`
}

type cacheRulePlan struct {
	PolicyScope string                  `json:"policy_scope"`
	Version     int                     `json:"expected_rule_set_version"`
	Before      cacheRuleView           `json:"before"`
	Proposed    api.EdgeRuleCacheAction `json:"proposed"`
}

type cacheRuleReceipt struct {
	AppID       string        `json:"app_id"`
	PolicyScope string        `json:"policy_scope"`
	Rule        cacheRuleView `json:"rule"`
}

func registerCacheRules(s *mcp.Server, m *management) {
	register(s, m, "gregale_plan_cache_rule", "Review cache settings for an existing unconditional app-wide cache rule. Changes shared app policy inherited by named environments. Requires unrestricted environment access; named/default-only grants cannot authorize it. Pins the native rule-set version for 15 minutes. No creation or selector changes.", m.planCacheRule)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_cache_rule", "Apply a reviewed shared-app cache plan with native If-Match and a stable replay key. Rechecks account, app and unrestricted environment write access. Retry the same plan after a lost response; receipt confirms desired policy, not gateway convergence or cache eviction.", false, m.applyCacheRule)
	}
}

func (m *management) cacheRuleAccess(project, environment string) error {
	if project != "" || environment != "" {
		return invalid("unsupported_scope", "cache rules are shared app policy; named environment writes are unavailable")
	}
	if m.opts.Environments != nil {
		return invalid("environment_access_denied", "shared app policy requires unrestricted environment access; default-only or named grants cannot authorize it")
	}
	return nil
}

// Project only the selected safe rule. Historical snapshots can contain secret
// conditions or actions on unrelated rules; none are returned or persisted.
func reviewCacheRule(r api.EdgeRuleResponse, app, account string) (cacheRuleView, error) {
	var out cacheRuleView
	if r.ID == "" || r.AppID != app || r.AccountID != account || r.Kind != "cache" || r.MatchHost != api.EdgeRuleAppHosts || !r.Enabled || r.Mode != api.EdgeRuleModeEnforce || r.Match != nil || len(r.MatchHeaders) != 0 || r.ExpiresAt != nil || r.Expired {
		return out, invalid("unsupported_rule", "select an enabled unconditional non-expiring enforce-mode app-wide cache rule owned by this app")
	}
	var action struct {
		Kind  string                   `json:"kind"`
		Cache *api.EdgeRuleCacheAction `json:"cache"`
	}
	if json.Unmarshal(r.Action, &action) != nil || action.Kind != "cache" || action.Cache == nil {
		return out, invalid("invalid_response", "cache action is missing from the native rule snapshot")
	}
	cache := *action.Cache
	validated := cache
	if validated.Validate() != nil {
		return out, invalid("invalid_response", "native cache settings are invalid")
	}
	return cacheRuleView{ID: r.ID, Host: r.MatchHost, Path: r.MatchPath, Methods: r.MatchMethods, Priority: r.Priority, Cache: cache}, nil
}

func (m *management) planCacheRule(ctx context.Context, args cacheRulePlanArgs) (any, error) {
	if err := m.cacheRuleAccess(args.Project, args.Environment); err != nil {
		return nil, err
	}
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if err := validateSegment(args.RuleID, "rule_id"); err != nil {
		return nil, err
	}
	if prob := args.Cache.Validate(); prob != nil {
		return nil, invalid("invalid_argument", prob.Detail)
	}
	// Collapse omitted empty arrays to the exact representation sent to apid.
	normalized, _ := json.Marshal(args.Cache)
	args.Cache = api.EdgeRuleCacheAction{}
	_ = json.Unmarshal(normalized, &args.Cache)
	app, _, err := m.appEnvironment(ctx, args.App, "", "", false)
	if err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" || app.ID == "" || !m.appIDAllowed(app.ID) {
		return nil, invalid("invalid_response", "account or app identity is missing or outside consent")
	}
	versions, err := m.client.ListEdgeRuleSetVersions(ctx, args.App)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 || versions[0].Version < 1 || !versions[0].Current {
		return nil, invalid("policy_unavailable", "a current native rule-set version is required")
	}
	snapshot, err := m.client.GetEdgeRuleSetVersion(ctx, args.App, versions[0].Version)
	if err != nil {
		return nil, err
	}
	if snapshot.Version != versions[0].Version || !snapshot.Current || snapshot.RulesSHA256 != versions[0].RulesSHA256 {
		return nil, invalid("plan_stale", "rule-set snapshot changed; create a new plan")
	}
	var selected *api.EdgeRuleResponse
	for i := range snapshot.Rules {
		if snapshot.Rules[i].ID == args.RuleID {
			if selected != nil {
				return nil, invalid("invalid_response", "duplicate native rule ID")
			}
			selected = &snapshot.Rules[i]
		}
	}
	if selected == nil {
		return nil, invalid("unsupported_rule", "rule is absent from the current app snapshot")
	}
	before, err := reviewCacheRule(*selected, app.ID, account.ID)
	if err != nil {
		return nil, err
	}
	if reflect.DeepEqual(before.Cache, args.Cache) {
		return nil, invalid("no_change", "cache settings already match the proposal")
	}
	p := &releasePlan{Action: "cache_rule", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: app.ID, App: args.App, Scope: cacheRulePolicyScope, Reason: args.Reason, TrafficEffect: "Changes shared app-wide cache policy, including inheritance by named environments. Does not evict existing entries or prove gateway convergence. Selectors, priority and enablement stay fixed.", ExpiresAt: time.Now().Add(releasePlanTTL), CacheRule: &cacheRulePlan{PolicyScope: cacheRulePolicyScope, Version: snapshot.Version, Before: before, Proposed: args.Cache}}
	return m.savePlan(p)
}

func (m *management) applyCacheRule(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.cacheRuleAccess("", ""); err != nil {
		return nil, err
	}
	p, err := m.checkedPlan(ctx, args.PlanID, "cache_rule")
	if err != nil {
		return nil, err
	}
	if p.CacheRule == nil {
		return nil, invalid("invalid_plan", "cache review is missing")
	}
	if p.cacheResult != nil {
		return *p.cacheResult, nil
	}
	p.attempted = true
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(p.CacheRule.Proposed)
	raw := json.RawMessage(body)
	// Replay the original request before checking current state: a successful
	// original write necessarily advanced the version. The native guard rejects
	// stale uncommitted attempts, while its receipt recovers committed replies.
	out, err := m.client.UpdateEdgeRuleIfMatch(ctx, p.CacheRule.Before.ID, p.CacheRule.Version, "mcp-cache-"+p.ID, api.UpdateEdgeRuleRequest{Action: &raw})
	if err != nil {
		return nil, err
	}
	view, err := reviewCacheRule(out, p.AppID, p.AccountID)
	if err != nil {
		return nil, err
	}
	expected := p.CacheRule.Before
	expected.Cache = p.CacheRule.Proposed
	if !reflect.DeepEqual(view, expected) {
		return nil, invalid("invalid_response", "saved cache rule differs from reviewed proposal")
	}
	receipt := cacheRuleReceipt{AppID: p.AppID, PolicyScope: cacheRulePolicyScope, Rule: view}
	p.cacheResult = &receipt
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return receipt, nil
}
