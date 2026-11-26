package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Event subscriptions and API consumers (ADR-20261118161500). Reads are
// app-scoped. Pausing or resuming a subscription, recovering events and
// changing consumers are reviewed operation plans applied by
// gregale_apply_operation. apid asks a person before production pauses and
// recoveries, consumer revocations, plan assignments and spend caps.

type eventHealthArgs struct {
	App          string `json:"app" jsonschema:"App slug"`
	Subscription string `json:"subscription" jsonschema:"Subscription ID from gregale_list_event_subscriptions"`
	Window       string `json:"window,omitempty" jsonschema:"5m, 15m, 1h, 6h or 24h; default 1h"`
}

type eventDeliveriesArgs struct {
	App     string `json:"app" jsonschema:"App slug"`
	EventID string `json:"event_id,omitempty" jsonschema:"Only this event's deliveries"`
	State   string `json:"state,omitempty" jsonschema:"Only deliveries in this state, such as failed or pending"`
	Limit   int    `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
	Before  string `json:"before,omitempty" jsonschema:"next_before from the previous page"`
}

type eventRecoveriesArgs struct {
	App   string `json:"app" jsonschema:"App slug"`
	State string `json:"state,omitempty" jsonschema:"Only recoveries in this state"`
	Limit int    `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
}

type planEventArgs struct {
	App           string         `json:"app" jsonschema:"App slug"`
	Op            string         `json:"op" jsonschema:"pause, resume, recover, cancel_recovery, set_schema_versions or reset_schema_versions"`
	Subscription  string         `json:"subscription,omitempty" jsonschema:"pause, resume, set_schema_versions, reset_schema_versions: the subscription"`
	Versions      []string       `json:"versions,omitempty" jsonschema:"set_schema_versions: the event schema versions the subscription accepts"`
	RatePerSecond int            `json:"rate_per_second,omitempty" jsonschema:"resume: optional delivery rate limit"`
	Recovery      map[string]any `json:"recovery,omitempty" jsonschema:"recover: what to replay, as POST /v1/apps/{slug}/event-recoveries takes it: mode, subscription_id, event_source, event_type, failure_code, outcome, min_age_seconds, rate_per_second, ..."`
	RecoveryID    string         `json:"recovery_id,omitempty" jsonschema:"cancel_recovery: the recovery job"`
	Reason        string         `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type eventChangeRequest struct {
	Op            string                    `json:"op"`
	Subscription  string                    `json:"subscription,omitempty"`
	RatePerSecond int                       `json:"rate_per_second,omitempty"`
	Recovery      *api.EventRecoveryRequest `json:"recovery,omitempty"`
	RecoveryID    string                    `json:"recovery_id,omitempty"`
	Versions      []string                  `json:"versions,omitempty"`
}

type consumerArgs struct {
	App      string `json:"app" jsonschema:"App slug"`
	Consumer string `json:"consumer" jsonschema:"Consumer ID from gregale_list_api_consumers"`
	Since    string `json:"since,omitempty" jsonschema:"Usage from this RFC 3339 time; default 24 hours ago"`
}

type planConsumerArgs struct {
	App                  string `json:"app" jsonschema:"App slug"`
	Op                   string `json:"op" jsonschema:"create, revoke, set_spend_cap or assign_plan"`
	Consumer             string `json:"consumer,omitempty" jsonschema:"revoke, set_spend_cap, assign_plan: the consumer"`
	ExternalRef          string `json:"external_ref,omitempty" jsonschema:"create: your system's ID for the customer"`
	Name                 string `json:"name,omitempty" jsonschema:"create: display name"`
	Currency             string `json:"currency,omitempty" jsonschema:"set_spend_cap: ISO currency such as usd"`
	MonthlyCapMillicents *int64 `json:"monthly_cap_millicents,omitempty" jsonschema:"set_spend_cap: monthly cap in millicents; omit to remove the cap"`
	PlanID               string `json:"plan_id,omitempty" jsonschema:"assign_plan: the consumer plan"`
	Reason               string `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type consumerChangeRequest struct {
	Op       string                             `json:"op"`
	Consumer string                             `json:"consumer,omitempty"`
	Create   *api.CreateAPIConsumerRequest      `json:"create,omitempty"`
	SpendCap *api.PutAPIConsumerSpendCapRequest `json:"spend_cap,omitempty"`
	PlanID   string                             `json:"plan_id,omitempty"`
}

func registerEventsConsumers(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_event_subscriptions", "List the app's event subscriptions: source, type, filter, work policy and whether each is enabled. Read-only.", m.listEventSubscriptions)
	register(s, m, "gregale_get_event_subscription_health", "Read one subscription's consumer and delivery health over a window: backlog, lag, failures and delivery control. Read-only.", m.getEventSubscriptionHealth)
	register(s, m, "gregale_list_event_deliveries", "List the app's event deliveries, newest first: event, subscription, state, attempts and last error. No payloads. Read-only.", m.listEventDeliveries)
	register(s, m, "gregale_list_event_recoveries", "List the app's event recovery jobs and their progress. Read-only.", m.listEventRecoveries)
	register(s, m, "gregale_plan_event_change", "Review pausing or resuming a subscription's deliveries, recovering (replaying) matched events, cancelling a recovery, or setting which event schema versions a subscription accepts. A recover plan shows how many events match. Changes nothing; apply with gregale_apply_operation. On a production app, pauses, recoveries and schema version changes ask a person first.", m.planEventChange)
	register(s, m, "gregale_list_api_consumers", "List the app's API consumers (your customers calling the app): name, external ref and status. Read-only.", m.listAPIConsumers)
	register(s, m, "gregale_get_api_consumer", "Read one API consumer with its usage, spend cap and plan assignments. Key values are never shown. Read-only.", m.getAPIConsumer)
	register(s, m, "gregale_get_mcp_seller_stats", "Read a sold MCP server's seller stats over 1 to 12 UTC calendar months: sign-ups by source (oauth, signup, owner), active, paying and closed customers, units, billed revenue (finalized statements before credits) and this month's estimate, units and revenue per tool, and customers at 80% or more of their plan's monthly units. Needs API monetization. Read-only.", m.getMCPSellerStats)
	register(s, m, "gregale_plan_api_consumer_change", "Review creating, revoking, capping the monthly spend of, or assigning a plan to an API consumer. Changes nothing; apply with gregale_apply_operation. Revoking, plans and spend caps ask a person first.", m.planAPIConsumerChange)
}

func (m *management) listEventSubscriptions(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	return m.client.ListAppsSlugEventSubscriptions(ctx, a.App)
}

// appSubscription checks the subscription belongs to the consented app.
func (m *management) appSubscription(ctx context.Context, app, id string) error {
	if err := validateSegment(id, "subscription"); err != nil {
		return err
	}
	if _, err := m.consentedApp(ctx, app, false); err != nil {
		return err
	}
	list, err := m.client.ListAppsSlugEventSubscriptions(ctx, app)
	if err != nil {
		return err
	}
	for _, s := range list.Subscriptions {
		if s.ID == id {
			return nil
		}
	}
	return invalid("app_access_denied", "the subscription is not one of the app's")
}

func (m *management) getEventSubscriptionHealth(ctx context.Context, a eventHealthArgs) (any, error) {
	window := a.Window
	if window == "" {
		window = "1h"
	}
	if _, err := api.EventConsumerHealthWindow(window); err != nil {
		return nil, invalid("invalid_argument", err.Error())
	}
	if err := m.appSubscription(ctx, a.App, a.Subscription); err != nil {
		return nil, err
	}
	consumer, err := m.client.GetEventConsumerHealth(ctx, a.App, a.Subscription, window)
	if err != nil {
		return nil, err
	}
	delivery, err := m.client.GetEventConsumerDeliveryHealth(ctx, a.App, a.Subscription, window)
	if err != nil {
		return nil, err
	}
	return map[string]any{"consumer": consumer, "delivery": delivery}, nil
}

func (m *management) listEventDeliveries(ctx context.Context, a eventDeliveriesArgs) (any, error) {
	limit, err := pageSize(a.Limit)
	if err != nil {
		return nil, err
	}
	for field, v := range map[string]string{"event_id": a.EventID, "state": a.State, "before": a.Before} {
		if v != "" {
			if err := validateSegment(v, field); err != nil {
				return nil, err
			}
		}
	}
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	return m.client.ListEventDeliveries(ctx, a.App, a.EventID, a.State, a.Before, limit)
}

func (m *management) listEventRecoveries(ctx context.Context, a eventRecoveriesArgs) (any, error) {
	limit, err := pageSize(a.Limit)
	if err != nil {
		return nil, err
	}
	if a.State != "" {
		if err := validateSegment(a.State, "state"); err != nil {
			return nil, err
		}
	}
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	return m.client.ListEventRecoveries(ctx, a.App, api.EventRecoveryListQuery{State: a.State, Limit: limit})
}

func (m *management) planEventChange(ctx context.Context, a planEventArgs) (any, error) {
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	req := eventChangeRequest{Op: a.Op}
	view := map[string]any{}
	effect := ""
	switch a.Op {
	case "pause", "resume":
		if a.Recovery != nil || a.RecoveryID != "" || (a.Op == "pause" && a.RatePerSecond != 0) || a.RatePerSecond < 0 {
			return nil, invalid("invalid_argument", "pause takes subscription; resume takes subscription and an optional rate_per_second")
		}
		if err := m.appSubscription(ctx, a.App, a.Subscription); err != nil {
			return nil, err
		}
		req.Subscription, req.RatePerSecond = a.Subscription, a.RatePerSecond
		effect = map[string]string{
			"pause":  "Stops delivering the subscription's events; they queue until resumed.",
			"resume": "Delivers the subscription's queued events again, from the oldest.",
		}[a.Op]
	case "set_schema_versions", "reset_schema_versions":
		if a.Recovery != nil || a.RecoveryID != "" || a.RatePerSecond != 0 || (a.Op == "reset_schema_versions") != (len(a.Versions) == 0) {
			return nil, invalid("invalid_argument", "set_schema_versions takes subscription and versions; reset_schema_versions takes subscription")
		}
		for _, v := range a.Versions {
			if err := validEventName(v, "versions"); err != nil {
				return nil, err
			}
		}
		if err := m.appSubscription(ctx, a.App, a.Subscription); err != nil {
			return nil, err
		}
		if before, err := m.client.GetEventSubscriptionSchemaVersions(ctx, a.App, a.Subscription); err == nil {
			view["schema_versions_before"] = before
		}
		req.Subscription, req.Versions = a.Subscription, a.Versions
		effect = map[string]string{
			"set_schema_versions":   "The subscription accepts only events of these schema versions; events of other versions are excluded from its deliveries.",
			"reset_schema_versions": "The subscription accepts events of every schema version again.",
		}[a.Op]
	case "recover":
		if a.Subscription != "" || a.RecoveryID != "" || a.RatePerSecond != 0 {
			return nil, invalid("invalid_argument", "recover takes only recovery")
		}
		var rec api.EventRecoveryRequest
		if err := strictDecode(a.Recovery, &rec, "recovery"); err != nil {
			return nil, err
		}
		if rec.ParentJobID != "" || rec.RequestID != "" {
			return nil, invalid("invalid_argument", "recovery sets parent_job_id and request_id itself")
		}
		rec.Reason = a.Reason
		preview, err := m.client.PreviewEventRecovery(ctx, a.App, rec)
		if err != nil {
			return nil, err
		}
		if preview.MatchedCount == 0 {
			return nil, invalid("nothing_to_recover", "no events match this recovery")
		}
		sample, err := m.redactPayloads(preview)
		if err != nil {
			return nil, err
		}
		req.Recovery = &rec
		view["preview"] = sample
		effect = fmt.Sprintf("Replays %d matched events through the app's current code: their side effects happen again.", preview.MatchedCount)
	case "cancel_recovery":
		if err := validateSegment(a.RecoveryID, "recovery_id"); err != nil {
			return nil, err
		}
		job, err := m.client.GetEventRecovery(ctx, a.RecoveryID)
		if err != nil {
			return nil, err
		}
		if job.ID != a.RecoveryID || job.AppID != app.ID {
			return nil, invalid("app_access_denied", "the recovery belongs to a different app")
		}
		req.RecoveryID = a.RecoveryID
		view["recovery"] = job
		effect = "Stops the recovery; events it already replayed stay replayed."
	default:
		return nil, invalid("invalid_argument", "op must be pause, resume, recover, cancel_recovery, set_schema_versions or reset_schema_versions")
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "event_change", req)
	if err != nil {
		return nil, err
	}
	view["request"] = req
	p.Operation.Changes, _ = json.Marshal(view)
	p.TrafficEffect = effect
	return m.savePlan(p)
}

func (m *management) runEventChange(ctx context.Context, p *releasePlan) (any, error) {
	var req eventChangeRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	switch req.Op {
	case "pause":
		return m.client.PauseEventSubscription(ctx, p.App, req.Subscription)
	case "resume":
		body := api.EventSubscriptionResumeRequest{}
		if req.RatePerSecond > 0 {
			body.RatePerSecond = &req.RatePerSecond
		}
		return m.client.ResumeEventSubscription(ctx, p.App, req.Subscription, body)
	case "recover":
		rec := *req.Recovery
		rec.RequestID = "mcp-operation-" + p.ID
		return m.client.CreateEventRecovery(ctx, p.App, rec)
	case "cancel_recovery":
		return m.client.CancelEventRecovery(ctx, req.RecoveryID)
	case "set_schema_versions":
		return m.client.SetEventSubscriptionSchemaVersions(ctx, p.App, req.Subscription, req.Versions)
	case "reset_schema_versions":
		return m.client.ResetEventSubscriptionSchemaVersions(ctx, p.App, req.Subscription)
	}
	return nil, invalid("invalid_plan", "unsupported event change")
}

// mcpSellerStatsArgs reads seller stats (ADR-20261126082046).
type mcpSellerStatsArgs struct {
	appArgs
	Months int `json:"months,omitempty" jsonschema:"UTC calendar months to read, 1 (this month, the default) to 12"`
}

func (m *management) getMCPSellerStats(ctx context.Context, a mcpSellerStatsArgs) (any, error) {
	if a.Months < 0 || a.Months > api.MCPSellerStatsMaxMonths {
		return nil, fmt.Errorf("months must be from 1 to %d", api.MCPSellerStatsMaxMonths)
	}
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	return m.client.GetMCPSellerStats(ctx, a.App, a.Months)
}

func (m *management) listAPIConsumers(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	return m.client.ListAPIConsumers(ctx, a.App)
}

// appConsumer returns a consumer of the consented app.
func (m *management) appConsumer(ctx context.Context, app, id string) (api.APIConsumerResponse, error) {
	if err := validateSegment(id, "consumer"); err != nil {
		return api.APIConsumerResponse{}, err
	}
	a, err := m.consentedApp(ctx, app, false)
	if err != nil {
		return api.APIConsumerResponse{}, err
	}
	c, err := m.client.GetAPIConsumer(ctx, app, id)
	if err != nil {
		return c, err
	}
	if c.ID != id || c.AppID != a.ID {
		return c, invalid("app_access_denied", "the consumer belongs to a different app")
	}
	return c, nil
}

func (m *management) getAPIConsumer(ctx context.Context, a consumerArgs) (any, error) {
	since := time.Now().UTC().Add(-24 * time.Hour)
	if a.Since != "" {
		t, err := time.Parse(time.RFC3339, a.Since)
		if err != nil {
			return nil, invalid("invalid_argument", "since must be an RFC 3339 time")
		}
		since = t
	}
	c, err := m.appConsumer(ctx, a.App, a.Consumer)
	if err != nil {
		return nil, err
	}
	usage, err := m.client.GetAPIConsumerUsage(ctx, a.App, a.Consumer, api.APIConsumerUsageOptions{Since: since.Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"consumer": c, "usage": usage}
	if spendCap, err := m.client.GetAPIConsumerSpendCap(ctx, a.App, a.Consumer); err == nil {
		out["spend_cap"] = spendCap
	}
	if plans, err := m.client.ListAPIConsumerPlanAssignments(ctx, a.App, a.Consumer); err == nil {
		out["plan_assignments"] = plans
	}
	return out, nil
}

func (m *management) planAPIConsumerChange(ctx context.Context, a planConsumerArgs) (any, error) {
	req := consumerChangeRequest{Op: a.Op}
	view := map[string]any{}
	effect := ""
	if a.Op != "create" {
		c, err := m.appConsumer(ctx, a.App, a.Consumer)
		if err != nil {
			return nil, err
		}
		req.Consumer = a.Consumer
		view["consumer"] = c
	} else if a.Consumer != "" {
		return nil, invalid("invalid_argument", "create takes no consumer")
	}
	switch a.Op {
	case "create":
		if a.ExternalRef == "" || a.Name == "" {
			return nil, invalid("invalid_argument", "create needs external_ref and name")
		}
		req.Create = &api.CreateAPIConsumerRequest{ExternalRef: a.ExternalRef, Name: a.Name}
		effect = "Registers a customer identity on the app; it holds no key until someone issues one."
	case "revoke":
		effect = "Revokes the consumer: its keys stop working at once."
	case "set_spend_cap":
		if a.Currency == "" || (a.MonthlyCapMillicents != nil && *a.MonthlyCapMillicents < 0) {
			return nil, invalid("invalid_argument", "set_spend_cap needs currency and a monthly_cap_millicents that is not negative, or no cap")
		}
		req.SpendCap = &api.PutAPIConsumerSpendCapRequest{Currency: a.Currency, MonthlyCapMillicents: a.MonthlyCapMillicents}
		if before, err := m.client.GetAPIConsumerSpendCap(ctx, a.App, a.Consumer); err == nil {
			view["spend_cap_before"] = before
		}
		effect = "Changes how much the consumer may spend each month; requests past the cap are refused."
	case "assign_plan":
		if err := validateSegment(a.PlanID, "plan_id"); err != nil {
			return nil, err
		}
		req.PlanID = a.PlanID
		effect = "Moves the consumer to the plan: its limits and prices apply from now."
	default:
		return nil, invalid("invalid_argument", "op must be create, revoke, set_spend_cap or assign_plan")
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "consumer_change", req)
	if err != nil {
		return nil, err
	}
	view["request"] = req
	p.Operation.Changes, _ = json.Marshal(view)
	p.TrafficEffect = effect
	return m.savePlan(p)
}

func (m *management) runConsumerChange(ctx context.Context, p *releasePlan) (any, error) {
	var req consumerChangeRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	switch req.Op {
	case "create":
		return m.client.CreateAPIConsumer(ctx, p.App, *req.Create)
	case "revoke":
		return m.client.RevokeAPIConsumer(ctx, p.App, req.Consumer)
	case "set_spend_cap":
		return m.client.PutAPIConsumerSpendCap(ctx, p.App, req.Consumer, *req.SpendCap)
	case "assign_plan":
		return m.client.AssignAPIConsumerPlan(ctx, p.App, req.Consumer, api.AssignAPIConsumerPlanRequest{PlanID: req.PlanID})
	}
	return nil, invalid("invalid_plan", "unsupported consumer change")
}
