package mcpmanagement

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onebox-faas/faas/pkg/api"
)

// Read-only product analytics tools (ADR 20261117084505): an agent reads the
// same event series, funnels, retention and per-variant request telemetry
// the `gregale insight` commands print.

type insightRangeArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
	From        string `json:"from,omitempty" jsonschema:"Range start, RFC 3339; defaults per tool"`
	Until       string `json:"until,omitempty" jsonschema:"Range end, RFC 3339; defaults to now"`
	Unit        string `json:"unit,omitempty" jsonschema:"customer (default) or subject"`
	By          string `json:"by,omitempty" jsonschema:"Breakdown: flag:KEY or property:KEY"`
}

type insightEventsArgs struct {
	insightRangeArgs
	Event    string            `json:"event" jsonschema:"Event name passed to track()"`
	Interval string            `json:"interval,omitempty" jsonschema:"hour or day (default)"`
	Where    map[string]string `json:"where,omitempty" jsonschema:"Property values the events must carry"`
}

type insightFunnelArgs struct {
	insightRangeArgs
	Steps  []api.InsightStepRequest `json:"steps" jsonschema:"2 to 6 steps in order, each an event and optional where filter"`
	Window string                   `json:"window,omitempty" jsonschema:"Time from first to last step, Go duration 1h to 720h; default 168h"`
}

type insightRetentionArgs struct {
	insightRangeArgs
	Start   api.InsightStepRequest `json:"start" jsonschema:"Event that puts a unit in a cohort"`
	Return  api.InsightStepRequest `json:"return" jsonschema:"Event that counts as returning"`
	Period  string                 `json:"period,omitempty" jsonschema:"day (default) or week"`
	Periods int                    `json:"periods,omitempty" jsonschema:"Periods after the cohort to report, 1 to 12"`
}

type insightRequestsArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
	Flag        string `json:"flag" jsonschema:"Flag key"`
	From        string `json:"from,omitempty" jsonschema:"Range start, RFC 3339; defaults to one day ago"`
	Until       string `json:"until,omitempty" jsonschema:"Range end, RFC 3339; defaults to now"`
}

const insightGuidance = "Units are distinct customers, or hashed subjects with unit=subject. A flag breakdown splits by the flag value the event carried; (none) is events sent without that flag, (other) merges values past the breakdown bound. truncated=true means the query hit the unit bound: narrow the range or add filters before drawing conclusions. These are observational numbers; use an experiment for causal claims."

func registerInsights(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_insight_events", "Read a product analytics series for one track() event: events, distinct units and summed value per hour or day, optionally filtered by properties and split by a flag's variant or a property. Read-only.", m.getInsightEvents)
	register(s, m, "gregale_get_insight_funnel", "Compute a product analytics funnel: units that did 2 to 6 events in order within a window, with conversion per step, optionally split by flag variant or property. Read-only.", m.getInsightFunnel)
	register(s, m, "gregale_get_insight_retention", "Compute product analytics retention: cohorts by first start event and the share that sent the return event in each later day or week, optionally split by flag variant or property. Read-only.", m.getInsightRetention)
	register(s, m, "gregale_get_insight_requests", "Read request telemetry by a flag's served value: requests, server errors and p95 latency per variant. Read-only.", m.getInsightRequests)
	register(s, m, "gregale_list_insight_cohorts", "List an environment's saved cohorts: definition, member count, the plan's member bound and the last refresh. Flag rules target a cohort with the gregale.cohort attribute. Read-only.", m.listInsightCohorts)
}

func parseInsightTime(raw, name string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC 3339 time", name)
	}
	return &t, nil
}

func (a insightRangeArgs) times() (from, until *time.Time, err error) {
	if err := validateFlagExperimentScope(a.Project, a.Environment); err != nil {
		return nil, nil, err
	}
	if from, err = parseInsightTime(a.From, "from"); err != nil {
		return nil, nil, err
	}
	if until, err = parseInsightTime(a.Until, "until"); err != nil {
		return nil, nil, err
	}
	return from, until, nil
}

func insightResult(name string, v any) map[string]any {
	return map[string]any{name: v, "guidance": insightGuidance}
}

func (m *management) getInsightEvents(ctx context.Context, args insightEventsArgs) (any, error) {
	if _, _, err := args.times(); err != nil {
		return nil, err
	}
	q := url.Values{}
	for k, v := range map[string]string{"event": args.Event, "interval": args.Interval, "unit": args.Unit, "by": args.By, "from": args.From, "until": args.Until} {
		if v != "" {
			q.Set(k, v)
		}
	}
	for k, v := range args.Where {
		q.Add("where", k+":"+v)
	}
	res, err := m.client.ProjectInsightEvents(ctx, args.Project, args.Environment, q)
	if err != nil {
		return nil, err
	}
	return insightResult("series", res), nil
}

func (m *management) getInsightFunnel(ctx context.Context, args insightFunnelArgs) (any, error) {
	from, until, err := args.times()
	if err != nil {
		return nil, err
	}
	res, err := m.client.ProjectInsightFunnel(ctx, args.Project, args.Environment, api.InsightFunnelRequest{
		Steps: args.Steps, From: from, Until: until, Window: args.Window, Unit: args.Unit, By: args.By})
	if err != nil {
		return nil, err
	}
	return insightResult("funnel", res), nil
}

func (m *management) getInsightRetention(ctx context.Context, args insightRetentionArgs) (any, error) {
	from, until, err := args.times()
	if err != nil {
		return nil, err
	}
	res, err := m.client.ProjectInsightRetention(ctx, args.Project, args.Environment, api.InsightRetentionRequest{
		Start: args.Start, Return: args.Return, From: from, Until: until, Period: args.Period, Periods: args.Periods, Unit: args.Unit, By: args.By})
	if err != nil {
		return nil, err
	}
	return insightResult("retention", res), nil
}

func (m *management) getInsightRequests(ctx context.Context, args insightRequestsArgs) (any, error) {
	if err := validateFlagExperimentScope(args.Project, args.Environment); err != nil {
		return nil, err
	}
	if _, err := parseInsightTime(args.From, "from"); err != nil {
		return nil, err
	}
	if _, err := parseInsightTime(args.Until, "until"); err != nil {
		return nil, err
	}
	q := url.Values{"flag": {args.Flag}}
	if args.From != "" {
		q.Set("from", args.From)
	}
	if args.Until != "" {
		q.Set("until", args.Until)
	}
	res, err := m.client.ProjectInsightRequests(ctx, args.Project, args.Environment, q)
	if err != nil {
		return nil, err
	}
	return insightResult("requests", res), nil
}

type insightCohortsArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
}

const insightCohortGuidance = "Members are recomputed hourly. truncated=true means more customers matched than max_members; the lowest customer IDs were kept. A refresh_error keeps the previous members. Rules target a cohort with {\"attribute\": \"gregale.cohort\", \"op\": \"in\", \"values\": [KEY]}; requests see a change within a minute."

func (m *management) listInsightCohorts(ctx context.Context, args insightCohortsArgs) (any, error) {
	if err := validateFlagExperimentScope(args.Project, args.Environment); err != nil {
		return nil, err
	}
	res, err := m.client.ListProjectInsightCohorts(ctx, args.Project, args.Environment)
	if err != nil {
		return nil, err
	}
	return map[string]any{"cohorts": res.Cohorts, "guidance": insightCohortGuidance}, nil
}
