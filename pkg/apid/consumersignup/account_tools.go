package consumersignup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Account tools (ADR-20261125194510): with the owner's opt-in, the gateway
// adds account_usage and account_plans to an MCP server's tool list for the
// callers it enrolled through OAuth, and asks apid for their answers through
// the Auth service's ConsumerAccountTool.

var (
	// ErrAccountToolsOff is the app's account tools being off, or API
	// monetization not released to the account.
	ErrAccountToolsOff = errors.New("consumer signup: account tools are off")
	// ErrAccountToolUnknown is a tool name that is not an account tool.
	ErrAccountToolUnknown = errors.New("consumer signup: unknown account tool")
)

// AccountToolsOn reports whether the app's account tools are on.
func (s *Service) AccountToolsOn(ctx context.Context, accountID, appID string) bool {
	set, err := s.accountToolSettings(ctx, accountID, appID)
	return err == nil && set.AccountTools
}

func (s *Service) accountToolSettings(ctx context.Context, accountID, appID string) (state.APIConsumerSignupSettings, error) {
	signups, ok := s.Host.Store().(state.APIConsumerSignupStore)
	if !ok || !s.Host.Released(accountID) {
		return state.APIConsumerSignupSettings{}, ErrAccountToolsOff
	}
	set, err := signups.GetAPIConsumerSignupSettings(ctx, accountID, appID)
	if errors.Is(err, state.ErrNotFound) {
		return state.APIConsumerSignupSettings{}, ErrAccountToolsOff
	}
	return set, err
}

// AccountTool returns account tool's JSON answer for consumerID of
// accountID's appID. Errors: ErrAccountToolUnknown, ErrAccountToolsOff and
// state.ErrNotFound for an app or consumer that is not the account's.
func (s *Service) AccountTool(ctx context.Context, accountID, appID, consumerID, tool string) ([]byte, error) {
	if !api.MCPAccountTool(tool) {
		return nil, ErrAccountToolUnknown
	}
	set, err := s.accountToolSettings(ctx, accountID, appID)
	if err != nil {
		return nil, fmt.Errorf("account tool: settings: %w", err)
	}
	if !set.AccountTools {
		return nil, ErrAccountToolsOff
	}
	app, err := s.Host.Store().AppByID(ctx, appID)
	if err == nil && app.AccountID != accountID {
		err = state.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("account tool: app: %w", err)
	}
	consumer, err := s.Host.Store().GetAPIConsumerByID(ctx, accountID, consumerID)
	if err == nil && consumer.AppID != appID {
		err = state.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("account tool: consumer: %w", err)
	}
	var out any
	if tool == api.MCPAccountToolUsage {
		out, err = s.accountUsage(ctx, app, consumer.ID)
	} else {
		out, err = s.accountPlans(ctx, app, set, consumer.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("account tool %s: %w", tool, err)
	}
	return json.Marshal(out)
}

// accountUsage is the consumer's plan, its limits and this UTC month's use.
func (s *Service) accountUsage(ctx context.Context, app state.App, consumerID string) (api.MCPAccountUsage, error) {
	now := s.now()
	out := api.MCPAccountUsage{PeriodStart: billing.MonthStart(now), Until: now.Truncate(time.Minute).Add(time.Minute),
		Tools: []api.MCPAccountToolUnits{}}
	if plans, ok := s.Host.Store().(state.APIConsumerPlanStore); ok {
		policy, err := plans.GetAPIConsumerPlanPolicy(ctx, app.AccountID, app.ID, consumerID, now)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return out, fmt.Errorf("plan policy: %w", err)
		}
		if err == nil && policy.PlanID != "" {
			plan, err := plans.GetAPIConsumerPlan(ctx, app.AccountID, app.ID, policy.PlanID)
			if err != nil {
				return out, fmt.Errorf("plan: %w", err)
			}
			out.Plan = &api.MCPAccountPlanLimits{Name: plan.Name, MaxRequestsPerMinute: policy.MaxRequestsPerMinute,
				MaxUnitsPerMonth: policy.MaxUnitsPerMonth, ToolLimits: mcpToolLimits(policy.RouteLimits)}
		}
	}
	if usage, ok := s.Host.Store().(state.ConsumerUsageStore); ok {
		buckets, err := usage.ListAPIConsumerUsage(ctx, app.AccountID, app.ID, consumerID, out.PeriodStart, out.Until)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return out, fmt.Errorf("usage: %w", err)
		}
		for _, b := range buckets {
			out.Requests += b.RequestCount
			out.Units += b.BillableUnits
		}
	}
	if out.Plan != nil && out.Plan.MaxUnitsPerMonth > 0 {
		left := max(0, out.Plan.MaxUnitsPerMonth-out.Units)
		out.UnitsLeft = &left
	}
	tools, err := s.toolUnits(ctx, app, consumerID, out.PeriodStart, out.Until)
	if err != nil {
		return out, err
	}
	out.Tools = tools
	return out, nil
}

// toolUnits sums the consumer's billable units per MCP tool, most used
// first.
func (s *Service) toolUnits(ctx context.Context, app state.App, consumerID string, since, until time.Time) ([]api.MCPAccountToolUnits, error) {
	routes, ok := s.Host.Store().(state.ConsumerRouteUsageStore)
	if !ok {
		return []api.MCPAccountToolUnits{}, nil
	}
	rows, err := routes.ListAPIConsumerRouteUsage(ctx, app.AccountID, app.ID, consumerID, since, until)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("tool usage: %w", err)
	}
	totals := map[string]int64{}
	for _, row := range rows {
		if tool, ok := strings.CutPrefix(row.Route, api.MCPBillingToolsPrefix+"/"); ok && tool != "" {
			totals[tool] += row.BillableUnits
		}
	}
	out := make([]api.MCPAccountToolUnits, 0, len(totals))
	for tool, units := range totals {
		out = append(out, api.MCPAccountToolUnits{Tool: tool, Units: units})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Units != out[j].Units {
			return out[i].Units > out[j].Units
		}
		return out[i].Tool < out[j].Tool
	})
	return out, nil
}

// mcpToolLimits keeps a plan's route limits that cap MCP tools, by tool.
func mcpToolLimits(limits map[string]state.APIConsumerPlanRouteLimit) map[string]api.APIConsumerPlanRouteLimit {
	out := map[string]api.APIConsumerPlanRouteLimit{}
	for route, limit := range limits {
		if tool, ok := strings.CutPrefix(route, api.MCPBillingToolsPrefix+"/"); ok && tool != "" {
			out[tool] = api.APIConsumerPlanRouteLimit{MaxRequestsPerMinute: limit.MaxRequestsPerMinute, MaxRequestsPerMonth: limit.MaxRequestsPerMonth}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// accountPlans is the offered plans, the consumer's, and its upgrade link.
func (s *Service) accountPlans(ctx context.Context, app state.App, set state.APIConsumerSignupSettings, consumerID string) (api.MCPAccountPlans, error) {
	plans, err := s.OfferedPlans(ctx, app, set)
	if err != nil {
		return api.MCPAccountPlans{}, fmt.Errorf("offered plans: %w", err)
	}
	out := api.MCPAccountPlans{Plans: plans}
	if store, ok := s.Host.Store().(state.APIConsumerPlanStore); ok {
		policy, err := store.GetAPIConsumerPlanPolicy(ctx, app.AccountID, app.ID, consumerID, s.now())
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return out, fmt.Errorf("plan policy: %w", err)
		}
		if err == nil && policy.PlanID != "" {
			plan, err := store.GetAPIConsumerPlan(ctx, app.AccountID, app.ID, policy.PlanID)
			if err != nil {
				return out, fmt.Errorf("plan: %w", err)
			}
			out.CurrentPlan = plan.Name
		}
	}
	link, expires, err := s.UpgradeLink(ctx, app.AccountID, app.ID, consumerID)
	if err != nil {
		return out, fmt.Errorf("upgrade link: %w", err)
	}
	if link != "" {
		out.UpgradeURL, out.UpgradeURLExpiresAt = link, &expires
	}
	return out, nil
}
