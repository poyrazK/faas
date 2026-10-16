package mcpmanagement

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type environmentScheduleActivityArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Owning project"`
	Environment string `json:"environment" jsonschema:"Named environment with current read consent"`
	After       string `json:"after,omitempty" jsonschema:"Saved cursor from the same account and immutable environment; omit to read from feed start"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Page size, default 20 and maximum 50"`
}

func validateScheduleEvent(e api.EnvironmentCacheScheduleEvent) error {
	if _, err := uuid.Parse(e.ScheduleID); err != nil {
		return invalid("invalid_response", "activity lacks a schedule UUID")
	}
	if e.Sequence < 1 || e.Sequence > api.RouteRequirementsMaxRevision || e.ExecuteAt.IsZero() || e.OccurredAt.IsZero() || len(e.ErrorCode) > 64 {
		return invalid("invalid_response", "invalid activity sequence, timestamps or failure code")
	}
	if e.RelatedScheduleID != "" {
		if _, err := uuid.Parse(e.RelatedScheduleID); err != nil || e.RelatedScheduleID == e.ScheduleID {
			return invalid("invalid_response", "invalid related schedule identity")
		}
	}
	switch e.Action {
	case "policy", "inheritance", "purge":
	default:
		return invalid("invalid_response", "unknown scheduled action")
	}
	switch e.Kind {
	case "accepted":
		if e.Status != "scheduled" {
			return invalid("invalid_response", "acceptance is not scheduled")
		}
	case "rescheduled":
		if e.Status != "cancelled" || e.RelatedScheduleID == "" {
			return invalid("invalid_response", "replacement lacks cancelled original or successor")
		}
	case "cancelled":
		if e.Status != "cancelled" {
			return invalid("invalid_response", "cancellation was not confirmed")
		}
	case "failed":
		if e.Status != "failed" || e.ErrorCode == "" {
			return invalid("invalid_response", "failure lacks stable outcome")
		}
	case "executed":
		if e.Status != "executed" {
			return invalid("invalid_response", "execution was not confirmed")
		}
		if e.Action == "purge" {
			if e.PurgeGeneration == nil || e.PolicyRevision != nil || *e.PurgeGeneration < 1 || *e.PurgeGeneration > api.RouteRequirementsMaxRevision {
				return invalid("invalid_response", "purge execution lacks its generation")
			}
		} else {
			if e.PolicyRevision == nil || e.PurgeGeneration != nil || *e.PolicyRevision < 1 || *e.PolicyRevision > api.RouteRequirementsMaxRevision {
				return invalid("invalid_response", "policy execution lacks its revision")
			}
		}
	default:
		return invalid("invalid_response", "unknown schedule activity kind")
	}
	if e.Kind != "failed" && e.ErrorCode != "" || e.Kind != "executed" && (e.PolicyRevision != nil || e.PurgeGeneration != nil) || e.Kind != "accepted" && e.Kind != "rescheduled" && e.RelatedScheduleID != "" {
		return invalid("invalid_response", "activity contains an inconsistent outcome")
	}
	return nil
}
func (m *management) getEnvironmentCacheScheduleActivity(ctx context.Context, args environmentScheduleActivityArgs) (any, error) {
	limit, err := api.NormalizeEnvironmentCacheActivityPage(args.After, args.Limit)
	if err != nil || args.Environment == "" {
		return nil, invalid("invalid_argument", "select a named environment and bounded activity page")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "account identity missing")
	}
	before, err := api.ParseEnvironmentCacheActivityCursor(args.After, account.ID, app.ID, env.ProjectID, env.ID)
	if err != nil {
		return nil, invalid("invalid_argument", err.Error())
	}
	out, err := m.client.GetEnvironmentCacheScheduleActivity(ctx, args.App, args.Environment, args.After, limit)
	if err != nil {
		return nil, m.validateActivityExpiry(ctx, err, args, account.ID, app.ID, env.ProjectID, env.ID, before.Sequence)
	}
	if out.AppID != app.ID || out.ProjectID != env.ProjectID || out.EnvironmentID != env.ID || out.Scope != args.Environment || out.Entries == nil || len(out.Entries) > limit || out.Cursor == "" || out.HasMore && len(out.Entries) != limit {
		return nil, invalid("invalid_response", "activity page exceeds its scope or bound")
	}
	sequence := before.Sequence
	for _, e := range out.Entries {
		if err = validateScheduleEvent(e); err != nil {
			return nil, err
		}
		if e.Sequence != sequence+1 {
			return nil, invalid("invalid_response", "activity is not contiguous after the saved cursor")
		}
		sequence = e.Sequence
	}
	next, err := api.ParseEnvironmentCacheActivityCursor(out.Cursor, account.ID, app.ID, env.ProjectID, env.ID)
	if err != nil || next.Sequence != sequence {
		return nil, invalid("invalid_response", "activity checkpoint differs from the returned page")
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return nil, invalid("plan_stale", "environment changed during activity inspection")
	}
	return out, nil
}
