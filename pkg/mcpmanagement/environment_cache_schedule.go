package mcpmanagement

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type environmentSchedulePlanArgs struct {
	App         string                      `json:"app" jsonschema:"Gregale app slug"`
	Project     string                      `json:"project" jsonschema:"Project owning the environment"`
	Environment string                      `json:"environment" jsonschema:"Named environment requiring read consent"`
	ExecuteAt   time.Time                   `json:"execute_at" jsonschema:"One-time execution timestamp with explicit timezone; at most 30 days ahead"`
	Reason      string                      `json:"reason" jsonschema:"Reviewable reason, at most 280 characters"`
	Rules       *[]api.EnvironmentCacheRule `json:"rules,omitempty" jsonschema:"Complete replacement rules; explicit empty list disables caching; omit for purge"`
	Inherited   bool                        `json:"inherited,omitempty" jsonschema:"Restore live app inheritance; requires explicit empty rules"`
	PurgePath   *string                     `json:"purge_path,omitempty" jsonschema:"Exact decoded path to purge; explicit empty string selects the whole environment; omit for policy"`
}
type environmentScheduleArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Owning project"`
	Environment string `json:"environment" jsonschema:"Exact named environment"`
	ScheduleID  string `json:"schedule_id" jsonschema:"Exact schedule UUID from schedule acceptance"`
}
type environmentSchedulePlan struct {
	Before   api.EnvironmentCachePolicy                `json:"before"`
	Proposed api.CreateEnvironmentCacheScheduleRequest `json:"proposed"`
}

func validateEnvironmentSchedulePlan(p *releasePlan) error {
	c := p.EnvironmentSchedule
	if c == nil || api.ValidateEnvironmentCacheSchedule(c.Proposed) != nil || c.Proposed.AppID != p.AppID || c.Proposed.ProjectID != p.ProjectID || c.Proposed.EnvironmentID != p.EnvironmentID || validateEnvironmentCachePolicy(c.Before, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil {
		return invalid("invalid_plan", "schedule target binding is invalid")
	}
	if c.Proposed.Policy != nil && *c.Proposed.Policy.ExpectedRevision != c.Before.Revision {
		return invalid("invalid_plan", "schedule policy guard differs from review")
	}
	return nil
}
func validateScheduleResponse(out api.EnvironmentCacheSchedule, app, scope, project, env, id string) error {
	if api.ValidateEnvironmentCacheSchedule(out.CreateEnvironmentCacheScheduleRequest) != nil || out.AppID != app || out.Scope != scope || out.ProjectID != project || out.EnvironmentID != env || out.ScheduleID != id || out.CreatedAt.IsZero() {
		return invalid("invalid_response", "schedule target or execution request is invalid")
	}

	for _, id := range []string{out.ReplacesScheduleID, out.ReplacedByScheduleID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil || id == out.ScheduleID {
				return invalid("invalid_response", "invalid replacement lineage")
			}
		}
	}
	if out.ReplacedByScheduleID != "" && out.Status != "cancelled" {
		return invalid("invalid_response", "replaced schedule is not cancelled")
	}
	switch out.Status {
	case "scheduled", "executing":
		if out.FinishedAt != nil || out.PolicyResult != nil || out.PurgeResult != nil || out.ErrorCode != "" {
			return invalid("invalid_response", "pending schedule contains a terminal result")
		}
	case "cancelled", "failed":
		if out.FinishedAt == nil || out.PolicyResult != nil || out.PurgeResult != nil || (out.Status == "failed" && out.ErrorCode == "") {
			return invalid("invalid_response", "invalid terminal schedule")
		}
	case "executed":
		if out.FinishedAt == nil || out.ErrorCode != "" {
			return invalid("invalid_response", "executed schedule lacks completion time")
		}
		if p := out.Policy; p != nil {
			if out.PolicyResult == nil || out.PurgeResult != nil || validateEnvironmentCachePolicy(*out.PolicyResult, app, scope, project, env) != nil || out.PolicyResult.Revision != *p.ExpectedRevision+1 || out.PolicyResult.Inherited != p.Inherited || !reflect.DeepEqual(out.PolicyResult.Rules, p.Rules) {
				return invalid("invalid_response", "scheduled policy result differs from action")
			}
		} else if p := out.Purge; p != nil {
			if out.PurgeResult == nil || out.PolicyResult != nil || validatePurgeReceipt(*out.PurgeResult, app, scope, project, env, id) != nil || out.PurgeResult.Path != p.Path {
				return invalid("invalid_response", "scheduled purge receipt differs from action")
			}
		}
	default:
		return invalid("invalid_response", "unknown schedule status")
	}
	return nil
}
func (m *management) planEnvironmentCacheSchedule(ctx context.Context, args environmentSchedulePlanArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if (args.Rules == nil) == (args.PurgePath == nil) || args.Inherited && args.Rules == nil || !args.ExecuteAt.After(time.Now()) || args.ExecuteAt.After(time.Now().Add(api.EnvironmentCacheScheduleMaxDelay)) {
		return nil, invalid("invalid_argument", "select exactly one action and a future time within 30 days")
	}
	value, err := m.getEnvironmentCachePolicy(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	before := value.(api.EnvironmentCachePolicy)
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "account identity missing")
	}
	req := api.CreateEnvironmentCacheScheduleRequest{ScheduleID: uuid.NewString(), AppID: before.AppID, ProjectID: before.ProjectID, EnvironmentID: before.EnvironmentID, ExecuteAt: args.ExecuteAt.UTC().Truncate(time.Microsecond)}
	if args.Rules != nil {
		rules, e := api.NormalizeEnvironmentCacheRules(*args.Rules)
		if e != nil {
			return nil, invalid("invalid_argument", e.Error())
		}
		req.Policy = &api.SetEnvironmentCachePolicyRequest{AppID: req.AppID, ProjectID: req.ProjectID, EnvironmentID: req.EnvironmentID, RequestID: req.ScheduleID, ExpectedRevision: &before.Revision, Rules: rules, Inherited: args.Inherited}
	} else {
		req.Purge = &api.CreateEnvironmentCachePurgeRequest{AppID: req.AppID, ProjectID: req.ProjectID, EnvironmentID: req.EnvironmentID, RequestID: req.ScheduleID, Path: *args.PurgePath}
	}
	p := &releasePlan{Action: "environment_cache_schedule", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: before.AppID, App: args.App, Project: args.Project, ProjectID: before.ProjectID, EnvironmentID: before.EnvironmentID, Scope: before.Scope, Reason: args.Reason, ExpiresAt: time.Now().Add(releasePlanTTL), TrafficEffect: "Delegates this exact one-time action to apid. Execution starts at or after execute_at, including recovery after outages. Policy execution rejects a changed revision; inherited mode follows live app rules. Cancellation is available only before worker claim. Schedule acceptance does not execute the action or establish gateway convergence. Disconnecting MCP or revoking its token does not cancel this durable delegation; cancel the schedule explicitly.", EnvironmentSchedule: &environmentSchedulePlan{Before: before, Proposed: req}}
	if err = validateEnvironmentSchedulePlan(p); err != nil {
		return nil, err
	}
	return m.savePlan(p)
}
func (m *management) applyEnvironmentCacheSchedule(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "environment_cache_schedule")
	if err != nil {
		return nil, err
	}
	if err = validateEnvironmentSchedulePlan(p); err != nil {
		return nil, err
	}
	if p.environmentScheduleResult != nil {
		return *p.environmentScheduleResult, nil
	}
	p.attempted = true
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	out, err := m.client.CreateEnvironmentCacheSchedule(ctx, p.App, p.Scope, p.EnvironmentSchedule.Proposed)
	if err != nil {
		return nil, err
	}
	if err = validateScheduleResponse(out, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID, p.EnvironmentSchedule.Proposed.ScheduleID); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(out.CreateEnvironmentCacheScheduleRequest, p.EnvironmentSchedule.Proposed) {
		return nil, invalid("invalid_response", "schedule acceptance differs from review")
	}
	p.environmentScheduleResult = &out
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	return out, nil
}
func (m *management) inspectEnvironmentCacheSchedule(ctx context.Context, args environmentScheduleArgs, cancel bool) (any, error) {
	if _, err := uuid.Parse(args.ScheduleID); err != nil || args.Environment == "" {
		return nil, invalid("invalid_argument", "select a named environment and schedule UUID")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, cancel)
	if err != nil {
		return nil, err
	}
	var out api.EnvironmentCacheSchedule
	if cancel {
		out, err = m.client.CancelEnvironmentCacheSchedule(ctx, args.App, args.Environment, args.ScheduleID)
	} else {
		out, err = m.client.GetEnvironmentCacheSchedule(ctx, args.App, args.Environment, args.ScheduleID)
	}
	if err != nil {
		return nil, err
	}
	if err = validateScheduleResponse(out, app.ID, args.Environment, env.ProjectID, env.ID, args.ScheduleID); err != nil {
		return nil, err
	}
	if cancel && out.Status != "cancelled" {
		return nil, invalid("invalid_response", "cancellation was not confirmed")
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, cancel)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return nil, invalid("plan_stale", "environment changed during schedule inspection")
	}
	result := map[string]any{"schedule": out}
	if !cancel && out.Status == "executed" {
		if out.PurgeResult != nil {
			value, e := m.getEnvironmentCachePurge(ctx, environmentPurgeStatusArgs{App: args.App, Project: args.Project, Environment: args.Environment, RequestID: out.ScheduleID})
			if e != nil {
				return nil, e
			}
			result["purge_status"] = value
		} else {
			evidence, e := m.client.GetEnvironmentCacheConvergence(ctx, args.App, args.Environment)
			if e != nil {
				return nil, e
			}
			if e = validateCacheConvergence(evidence, app.ID, args.Environment, env.ProjectID, env.ID); e != nil {
				return nil, e
			}
			if evidence.Revision != out.PolicyResult.Revision {
				result["convergence_status"] = "policy_superseded"
			} else {
				result["policy_convergence"] = evidence
			}
		}
	}
	return result, nil
}
func (m *management) getEnvironmentCacheSchedule(ctx context.Context, args environmentScheduleArgs) (any, error) {
	return m.inspectEnvironmentCacheSchedule(ctx, args, false)
}
func (m *management) cancelEnvironmentCacheSchedule(ctx context.Context, args environmentScheduleArgs) (any, error) {
	return m.inspectEnvironmentCacheSchedule(ctx, args, true)
}
