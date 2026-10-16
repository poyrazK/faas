package mcpmanagement

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type environmentScheduleListArgs struct {
	App         string     `json:"app" jsonschema:"Gregale app slug"`
	Project     string     `json:"project" jsonschema:"Owning project"`
	Environment string     `json:"environment" jsonschema:"Named environment with read consent"`
	Status      string     `json:"status,omitempty" jsonschema:"Optional scheduled, executing, executed, failed or cancelled filter"`
	From        *time.Time `json:"from,omitempty" jsonschema:"Inclusive execution timestamp lower bound"`
	Until       *time.Time `json:"until,omitempty" jsonschema:"Exclusive execution timestamp upper bound"`
	After       string     `json:"after,omitempty" jsonschema:"Opaque next_cursor returned for the same environment and filters"`
	Limit       int        `json:"limit,omitempty" jsonschema:"Page size, default 20 and maximum 50"`
}
type environmentRescheduleArgs struct {
	App         string    `json:"app" jsonschema:"Gregale app slug"`
	Project     string    `json:"project" jsonschema:"Owning project"`
	Environment string    `json:"environment" jsonschema:"Exact named environment"`
	ScheduleID  string    `json:"schedule_id" jsonschema:"Pending schedule UUID to replace"`
	ExecuteAt   time.Time `json:"execute_at" jsonschema:"New execution time, within 30 days; action and policy guard remain unchanged"`
	Reason      string    `json:"reason" jsonschema:"Reviewable rescheduling reason, at most 280 characters"`
}
type environmentReschedulePlan struct {
	Before   api.EnvironmentCacheSchedule          `json:"before"`
	Proposed api.RescheduleEnvironmentCacheRequest `json:"proposed"`
}

func validateEnvironmentReschedulePlan(p *releasePlan) error {
	c := p.EnvironmentReschedule
	if c == nil || c.Before.Status != "scheduled" || validateScheduleResponse(c.Before, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID, c.Before.ScheduleID) != nil || api.ValidateRescheduleEnvironmentCache(c.Before.ScheduleID, c.Proposed) != nil || c.Proposed.AppID != p.AppID || c.Proposed.ProjectID != p.ProjectID || c.Proposed.EnvironmentID != p.EnvironmentID || !c.Before.ExecuteAt.Equal(c.Proposed.ExpectedExecuteAt) {
		return invalid("invalid_plan", "reschedule review binding is invalid")
	}
	return nil
}
func validateRescheduleReceipt(p *releasePlan, out api.EnvironmentCacheSchedule) error {
	c := p.EnvironmentReschedule
	if validateEnvironmentReschedulePlan(p) != nil || validateScheduleResponse(out, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID, c.Proposed.ReplacementID) != nil || out.ReplacesScheduleID != c.Before.ScheduleID {
		return invalid("invalid_response", "replacement lacks exact reviewed identity")
	}
	expected, err := api.EnvironmentCacheScheduleReplacement(c.Before, c.Proposed.ReplacementID, c.Proposed.ExecuteAt)
	if err != nil || !reflect.DeepEqual(expected, out.CreateEnvironmentCacheScheduleRequest) {
		return invalid("invalid_response", "replacement action differs from review")
	}
	return nil
}
func (m *management) listEnvironmentCacheSchedules(ctx context.Context, args environmentScheduleListArgs) (any, error) {
	o, err := api.NormalizeEnvironmentCacheScheduleList(api.EnvironmentCacheScheduleListOptions{Status: args.Status, From: args.From, Until: args.Until, After: args.After, Limit: args.Limit})
	if err != nil {
		return nil, invalid("invalid_argument", err.Error())
	}
	if args.Environment == "" {
		return nil, invalid("invalid_argument", "select a named environment")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	c, err := api.ParseEnvironmentCacheScheduleCursor(o, app.ID, env.ProjectID, env.ID)
	if err != nil {
		return nil, invalid("invalid_argument", err.Error())
	}
	out, err := m.client.ListEnvironmentCacheSchedules(ctx, args.App, args.Environment, o)
	if err != nil {
		return nil, err
	}
	if out.AppID != app.ID || out.ProjectID != env.ProjectID || out.EnvironmentID != env.ID || out.Scope != args.Environment || out.Entries == nil || len(out.Entries) > o.Limit {
		return nil, invalid("invalid_response", "schedule page is outside the selected target or bound")
	}
	previousAt, previousID := c.ExecuteAt, c.ScheduleID
	for _, v := range out.Entries {
		if validateScheduleResponse(v, app.ID, args.Environment, env.ProjectID, env.ID, v.ScheduleID) != nil || o.Status != "" && v.Status != o.Status || o.From != nil && v.ExecuteAt.Before(*o.From) || o.Until != nil && !v.ExecuteAt.Before(*o.Until) || !previousAt.IsZero() && (v.ExecuteAt.Before(previousAt) || v.ExecuteAt.Equal(previousAt) && v.ScheduleID <= previousID) {
			return nil, invalid("invalid_response", "schedule page contains an invalid, unordered or unfiltered entry")
		}
		previousAt, previousID = v.ExecuteAt, v.ScheduleID
	}
	if out.NextCursor != "" {
		nextOptions := o
		nextOptions.After = out.NextCursor
		next, e := api.ParseEnvironmentCacheScheduleCursor(nextOptions, app.ID, env.ProjectID, env.ID)
		if e != nil || len(out.Entries) == 0 || !next.ExecuteAt.Equal(previousAt) || next.ScheduleID != previousID {
			return nil, invalid("invalid_response", "schedule continuation does not match its page")
		}
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return nil, invalid("plan_stale", "environment changed during schedule discovery")
	}
	return out, nil
}
func (m *management) planEnvironmentCacheReschedule(ctx context.Context, args environmentRescheduleArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(args.ScheduleID); err != nil || args.Environment == "" || !args.ExecuteAt.After(time.Now()) || args.ExecuteAt.After(time.Now().Add(api.EnvironmentCacheScheduleMaxDelay)) {
		return nil, invalid("invalid_argument", "select a pending schedule and future time within 30 days")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	before, err := m.client.GetEnvironmentCacheSchedule(ctx, args.App, args.Environment, args.ScheduleID)
	if err != nil {
		return nil, err
	}
	if err = validateScheduleResponse(before, app.ID, args.Environment, env.ProjectID, env.ID, args.ScheduleID); err != nil {
		return nil, err
	}
	if before.Status != "scheduled" {
		return nil, invalid("plan_stale", "only pending schedules can be rescheduled")
	}
	value, err := m.getEnvironmentCachePolicy(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	current := value.(api.EnvironmentCachePolicy)
	if current.AppID != app.ID || current.EnvironmentID != env.ID || current.ProjectID != env.ProjectID || before.Policy != nil && *before.Policy.ExpectedRevision != current.Revision {
		return nil, invalid("plan_stale", "environment or policy guard changed; review a new action")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "missing account identity")
	}
	req := api.RescheduleEnvironmentCacheRequest{ReplacementID: uuid.NewString(), AppID: app.ID, ProjectID: env.ProjectID, EnvironmentID: env.ID, ExpectedExecuteAt: before.ExecuteAt, ExecuteAt: args.ExecuteAt.UTC().Truncate(time.Microsecond)}
	p := &releasePlan{Action: "environment_cache_reschedule", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: app.ID, App: args.App, Project: args.Project, ProjectID: env.ProjectID, EnvironmentID: env.ID, Scope: args.Environment, Reason: args.Reason, ExpiresAt: time.Now().Add(releasePlanTTL), TrafficEffect: "Atomically cancels the pending original and creates a linked replacement at the reviewed time. Action, immutable target and original policy revision guard are preserved. A worker claim, cancellation or another replacement rejects this review. Acceptance is durable delegation, not execution or gateway convergence. Retry this same plan after a lost reply; cancel the replacement UUID to withdraw it.", EnvironmentReschedule: &environmentReschedulePlan{Before: before, Proposed: req}}
	if err = validateEnvironmentReschedulePlan(p); err != nil {
		return nil, err
	}
	return m.savePlan(p)
}
func (m *management) applyEnvironmentCacheReschedule(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "environment_cache_reschedule")
	if err != nil {
		return nil, err
	}
	if err = validateEnvironmentReschedulePlan(p); err != nil {
		return nil, err
	}
	if p.environmentRescheduleResult != nil {
		return *p.environmentRescheduleResult, nil
	}
	p.attempted = true
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	out, err := m.client.RescheduleEnvironmentCache(ctx, p.App, p.Scope, p.EnvironmentReschedule.Before.ScheduleID, p.EnvironmentReschedule.Proposed)
	if err != nil {
		return nil, err
	}
	if err = validateRescheduleReceipt(p, out); err != nil {
		return nil, err
	}
	p.environmentRescheduleResult = &out
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	return out, nil
}
