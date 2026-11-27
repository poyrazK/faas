package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Queues, workflow recovery and monitoring (ADR-20261117232109). Reads are
// app-scoped and return customer payloads only with data:read consent.
// Replays, workflow recovery and monitoring changes are reviewed operation
// plans applied by gregale_apply_operation, whose idempotency key makes a
// retried apply repeat nothing. apid asks a person before production
// replays and workflow recovery.

const (
	maxReplayIDs = 50
	// maxReplayAll matches apid's dead-letter replay limit.
	maxReplayAll = 200
	// maxReplayPreviewPages bounds the pages read to find the oldest dead
	// letters a replay-all takes.
	maxReplayPreviewPages = 10
	maxCancelQueuedRuns   = 50
	maxIncidentNote       = 4000
)

// payloadKeys hold customer data in DLQ events and workflow runs and steps.
var payloadKeys = []string{"payload", "headers", "input", "output"}

type appPageArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Limit  int    `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
	Before string `json:"before,omitempty" jsonschema:"next_before from the previous page"`
}

type workflowRunsArgs struct {
	App      string `json:"app" jsonschema:"App slug"`
	Status   string `json:"status,omitempty" jsonschema:"Filter: pending, running, succeeded, failed, cancelled, ..."`
	Workflow string `json:"workflow,omitempty" jsonschema:"Filter by workflow name"`
	Limit    int    `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
	Offset   int    `json:"offset,omitempty"`
}

type workflowRunArgs struct {
	App   string `json:"app" jsonschema:"App slug"`
	RunID string `json:"run_id" jsonschema:"Workflow run ID"`
}

type sloStatusArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Window string `json:"window,omitempty" jsonschema:"Request SLO window such as 1h, 24h or 7d; default 24h"`
}

type incidentsArgs struct {
	App      string `json:"app" jsonschema:"App slug"`
	OpenOnly bool   `json:"open_only,omitempty" jsonschema:"Only open incidents"`
}

type incidentIDArgs struct {
	ID string `json:"incident_id" jsonschema:"Incident ID from gregale_list_incidents"`
}

type incidentNoteArgs struct {
	ID   string `json:"incident_id" jsonschema:"Incident ID"`
	Body string `json:"body" jsonschema:"Note text, at most 4000 characters: what you found or did"`
}

type planReplayArgs struct {
	App      string   `json:"app" jsonschema:"App slug"`
	EventIDs []string `json:"event_ids,omitempty" jsonschema:"Up to 50 dead-letter event IDs to replay; or set all"`
	All      bool     `json:"all,omitempty" jsonschema:"Replay the oldest dead letters, up to limit"`
	Limit    int      `json:"limit,omitempty" jsonschema:"With all: 1 to 200; default 100"`
	Reason   string   `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type replayRequest struct {
	EventIDs []string `json:"event_ids,omitempty"`
	All      bool     `json:"all,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

type planWorkflowRecoveryArgs struct {
	App    string   `json:"app" jsonschema:"App slug"`
	Action string   `json:"action" jsonschema:"retry_step, resume, cancel or cancel_queued"`
	RunID  string   `json:"run_id,omitempty" jsonschema:"retry_step, resume, cancel: the run"`
	Step   string   `json:"step,omitempty" jsonschema:"retry_step: the failed step's name"`
	RunIDs []string `json:"run_ids,omitempty" jsonschema:"cancel_queued: up to 50 runs that have not started"`
	Reason string   `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type workflowRecoveryRequest struct {
	Action              string   `json:"action"`
	RunID               string   `json:"run_id,omitempty"`
	Step                string   `json:"step,omitempty"`
	RunIDs              []string `json:"run_ids,omitempty"`
	ExpectedResumeCount int      `json:"expected_resume_count,omitempty"`
}

// alertSpec is the part of an alert rule an agent may set: notify-only rules
// on existing channels. No webhook URL or secret, and no action.
type alertSpec struct {
	Name             *string   `json:"name,omitempty" jsonschema:"Rule name"`
	Metric           string    `json:"metric,omitempty" jsonschema:"create: metric such as error_rate, p95_latency_ms, slo_burn_rate, synthetic_failure"`
	Comparison       *string   `json:"comparison,omitempty" jsonschema:"gt, gte, lt or lte"`
	Threshold        *float64  `json:"threshold,omitempty"`
	WindowSpec       *string   `json:"window_spec,omitempty" jsonschema:"Evaluation window such as 5m"`
	SLOID            string    `json:"slo_id,omitempty" jsonschema:"create: SLO the rule watches"`
	SyntheticCheckID string    `json:"synthetic_check_id,omitempty" jsonschema:"create: synthetic check the rule watches"`
	ChannelIDs       *[]string `json:"channel_ids,omitempty" jsonschema:"Existing notification channel IDs; create needs at least one"`
	CooldownMinutes  *int      `json:"cooldown_minutes,omitempty"`
	Enabled          *bool     `json:"enabled,omitempty"`
}

type planMonitoringArgs struct {
	App       string                           `json:"app" jsonschema:"App slug"`
	Kind      string                           `json:"kind" jsonschema:"slo, synthetic or alert"`
	Op        string                           `json:"op" jsonschema:"create; synthetic: enable or disable; alert: update"`
	ID        string                           `json:"id,omitempty" jsonschema:"Existing synthetic or alert rule ID for enable, disable or update"`
	SLO       *api.CreateSLORequest            `json:"slo,omitempty" jsonschema:"create slo: name, sli (availability or latency), latency_threshold_ms, objective_pct, window_days"`
	Synthetic *api.CreateSyntheticCheckRequest `json:"synthetic,omitempty" jsonschema:"create synthetic: name, method, path, expected_status, timeout_ms, interval_seconds"`
	Alert     *alertSpec                       `json:"alert,omitempty"`
	Reason    string                           `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type monitoringRequest struct {
	Kind      string                           `json:"kind"`
	Op        string                           `json:"op"`
	ID        string                           `json:"id,omitempty"`
	SLO       *api.CreateSLORequest            `json:"slo,omitempty"`
	Synthetic *api.CreateSyntheticCheckRequest `json:"synthetic,omitempty"`
	Alert     *alertSpec                       `json:"alert,omitempty"`
}

func registerAsyncMonitoring(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_queue_state", "Read the app's queue: depth, in-flight messages, oldest pending age, plan cap and backlog by source. Read-only.", m.getQueueState)
	register(s, m, "gregale_list_dead_letters", "List the app's dead-lettered events, newest first: source, error kind and detail, retry count and times. Message bodies need data:read consent. Read-only.", m.listDeadLetters)
	register(s, m, "gregale_list_workflow_runs", "List the app's workflow runs, filtered by status or workflow. Inputs and outputs need data:read consent. Read-only.", m.listWorkflowRuns)
	register(s, m, "gregale_get_workflow_run", "Read one workflow run with its steps and diagnostics: current or failing step, errors, attempts. Inputs and outputs need data:read consent. Read-only.", m.getWorkflowRun)
	register(s, m, "gregale_list_workflow_attention", "List the app's workflow runs that need attention: blocked, failing or overdue, with the reason. Read-only.", m.listWorkflowAttention)
	register(s, m, "gregale_list_alerts", "List the app's alert rules: metric, threshold, window, channels and whether each is enabled. Read-only.", m.listAlerts)
	register(s, m, "gregale_get_slo_status", "Read the app's SLOs with their current attainment and error budget burn, and the app's request SLO for a window. Read-only.", m.getSLOStatus)
	register(s, m, "gregale_list_synthetics", "List the app's synthetic checks with recent results. Read-only.", m.listSynthetics)
	register(s, m, "gregale_list_incidents", "List the app's incidents (fired alerts, route monitor and other sources), optionally only open ones. Read-only.", m.listIncidents)
	register(s, m, "gregale_get_incident", "Read one incident: fires, updates, notes, recent changes and log context. Read-only.", m.getIncident)
	register(s, m, "gregale_plan_dead_letter_replay", "Review replaying dead letters: up to 50 named events, or the oldest up to 200. A replay runs the app's code again on the old input. Changes nothing; apply with gregale_apply_operation. On a production app an agent connection asks a person first.", m.planDeadLetterReplay)
	register(s, m, "gregale_plan_workflow_recovery", "Review recovering workflow runs: retry a failed step, resume a run, cancel a run, or cancel runs that have not started. Changes nothing; apply with gregale_apply_operation. Agent connections ask a person first.", m.planWorkflowRecovery)
	register(s, m, "gregale_plan_monitoring_change", "Review creating an SLO, creating, enabling or disabling a synthetic check, or creating or updating a notify-only alert rule on existing channels. Changes nothing; apply with gregale_apply_operation. Deletes stay with people.", m.planMonitoringChange)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_acknowledge_incident", "Acknowledge an open incident of a consented app, so people see someone is on it. Idempotent.", false, m.acknowledgeIncident)
		registerTool(s, m, "gregale_add_incident_note", "Add a note to an incident's timeline: what you found, what you changed and what is left.", false, m.addIncidentNote)
	}
}

// redactPayloads replaces customer payloads with their size unless the
// connection has data:read consent.
func (m *management) redactPayloads(v any) (any, error) {
	return m.redactKeys(v, payloadKeys)
}

// redactKeys replaces the values under keys with their size unless the
// connection has data:read consent.
func (m *management) redactKeys(v any, keys []string) (any, error) {
	if m.hasCapability(api.ScopeDataRead) {
		return v, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	return redactTree(tree, keys), nil
}

func redactTree(v any, keys []string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if slices.Contains(keys, k) && child != nil {
				b, _ := json.Marshal(child)
				t[k] = map[string]any{"redacted": "data:read consent required", "bytes": len(b)}
				continue
			}
			t[k] = redactTree(child, keys)
		}
	case []any:
		for i := range t {
			t[i] = redactTree(t[i], keys)
		}
	}
	return v
}

func (m *management) consentedApp(ctx context.Context, app string, write bool) (api.AppResponse, error) {
	a, _, err := m.appEnvironment(ctx, app, "", "", write)
	if err != nil {
		return a, err
	}
	if !m.appIDAllowed(a.ID) {
		return a, invalid("app_access_denied", "app identity is outside consent")
	}
	return a, nil
}

func pageSize(limit int) (int, error) {
	if limit == 0 {
		return 20, nil
	}
	if limit < 1 || limit > 100 {
		return 0, invalid("invalid_argument", "limit must be from 1 to 100")
	}
	return limit, nil
}

func (m *management) getQueueState(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	return m.client.QueueState(ctx, a.App)
}

func (m *management) listDeadLetters(ctx context.Context, a appPageArgs) (any, error) {
	limit, err := pageSize(a.Limit)
	if err != nil {
		return nil, err
	}
	if a.Before != "" {
		if err := validateSegment(a.Before, "before"); err != nil {
			return nil, err
		}
	}
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.ListDeadLetterEvents(ctx, a.App, limit, a.Before)
	if err != nil {
		return nil, err
	}
	for _, e := range out.Events {
		if e.AppID != "" && e.AppID != app.ID {
			return nil, invalid("invalid_response", "dead letter belongs to a different app")
		}
	}
	return m.redactPayloads(map[string]any{"items": out.Events, "total": len(out.Events), "next_before": out.NextBefore})
}

func (m *management) listWorkflowRuns(ctx context.Context, a workflowRunsArgs) (any, error) {
	limit, err := pageSize(a.Limit)
	if err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, invalid("invalid_argument", "offset must not be negative")
	}
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.ListWorkflowRunsWithOptions(ctx, a.App, api.WorkflowRunListOptions{Status: a.Status, WorkflowName: a.Workflow, Limit: limit, Offset: a.Offset})
	if err != nil {
		return nil, err
	}
	for _, r := range out.Runs {
		if r.AppID != app.ID {
			return nil, invalid("invalid_response", "workflow run belongs to a different app")
		}
	}
	return m.redactPayloads(map[string]any{"items": out.Runs, "total": out.Total})
}

// workflowRunOf reads a run and checks it belongs to the consented app.
func (m *management) workflowRunOf(ctx context.Context, app, runID string, write bool) (api.WorkflowRunResponse, error) {
	if err := validateSegment(runID, "run_id"); err != nil {
		return api.WorkflowRunResponse{}, err
	}
	a, err := m.consentedApp(ctx, app, write)
	if err != nil {
		return api.WorkflowRunResponse{}, err
	}
	run, err := m.client.GetWorkflowRun(ctx, runID)
	if err != nil {
		return run, err
	}
	if run.AppID != a.ID || run.ID != runID {
		return run, invalid("app_access_denied", "the workflow run belongs to a different app")
	}
	return run, nil
}

func (m *management) getWorkflowRun(ctx context.Context, a workflowRunArgs) (any, error) {
	run, err := m.workflowRunOf(ctx, a.App, a.RunID, false)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"run": run}
	if steps, err := m.client.ListWorkflowSteps(ctx, a.RunID); err == nil {
		out["steps"] = steps
	}
	if diagnostics, err := m.client.GetWorkflowRunDiagnostics(ctx, a.RunID); err == nil {
		out["diagnostics"] = diagnostics
	}
	return m.redactPayloads(out)
}

func (m *management) listWorkflowAttention(ctx context.Context, a appArgs) (any, error) {
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.ListAccountWorkflowAttention(ctx, a.App, api.OperationWorkflowAttentionOptions{AppID: app.ID, Scope: api.DefaultEnvScope, Limit: 50})
	if err != nil {
		return nil, err
	}
	return m.redactPayloads(out)
}

func (m *management) listAlerts(ctx context.Context, a appArgs) (any, error) {
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	rules, err := m.client.ListAlertRules(ctx, a.App)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.AppID != app.ID {
			return nil, invalid("invalid_response", "alert rule belongs to a different app")
		}
	}
	return map[string]any{"items": rules, "total": len(rules)}, nil
}

func (m *management) getSLOStatus(ctx context.Context, a sloStatusArgs) (any, error) {
	if a.Window != "" && !slices.Contains([]string{"1h", "6h", "24h", "7d", "30d"}, a.Window) {
		return nil, invalid("invalid_argument", "window must be 1h, 6h, 24h, 7d or 30d")
	}
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	slos, err := m.client.ListSLOs(ctx, a.App)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"slos": slos}
	if request, err := m.client.GetAppSLO(ctx, a.App, a.Window); err == nil {
		out["request_slo"] = request
	}
	return out, nil
}

func (m *management) listSynthetics(ctx context.Context, a appArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	checks, err := m.client.ListSyntheticChecks(ctx, a.App)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": checks, "total": len(checks)}, nil
}

func (m *management) listIncidents(ctx context.Context, a incidentsArgs) (any, error) {
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	out, err := m.client.ListIncidents(ctx, api.IncidentsQuery{App: a.App, OpenOnly: a.OpenOnly})
	if err != nil {
		return nil, err
	}
	items := slices.DeleteFunc(slices.Clone(out.Incidents), func(i api.Incident) bool { return i.App != a.App })
	return map[string]any{"items": items, "total": len(items), "truncated": out.Truncated, "since": out.Since}, nil
}

// incidentOf reads an incident and checks its app is in consent.
func (m *management) incidentOf(ctx context.Context, id string, write bool) (api.IncidentDetail, error) {
	if err := validateSegment(id, "incident_id"); err != nil {
		return api.IncidentDetail{}, err
	}
	detail, err := m.client.GetIncident(ctx, id)
	if err != nil {
		return detail, err
	}
	if detail.Incident.App == "" {
		return detail, invalid("app_access_denied", "the incident names no app")
	}
	if _, err := m.consentedApp(ctx, detail.Incident.App, write); err != nil {
		return detail, err
	}
	return detail, nil
}

func (m *management) getIncident(ctx context.Context, a incidentIDArgs) (any, error) {
	return m.incidentOf(ctx, a.ID, false)
}

func (m *management) acknowledgeIncident(ctx context.Context, a incidentIDArgs) (any, error) {
	if _, err := m.incidentOf(ctx, a.ID, true); err != nil {
		return nil, err
	}
	return m.client.AcknowledgeIncident(ctx, a.ID)
}

func (m *management) addIncidentNote(ctx context.Context, a incidentNoteArgs) (any, error) {
	if strings.TrimSpace(a.Body) == "" || !utf8.ValidString(a.Body) || utf8.RuneCountInString(a.Body) > maxIncidentNote {
		return nil, invalid("invalid_argument", "body must hold 1 to 4000 characters")
	}
	if _, err := m.incidentOf(ctx, a.ID, true); err != nil {
		return nil, err
	}
	return m.client.AddIncidentNote(ctx, a.ID, m.clean(a.Body))
}

func validIDs(ids []string, field string, max int) error {
	if len(ids) == 0 || len(ids) > max {
		return invalid("invalid_argument", fmt.Sprintf("%s must hold 1 to %d IDs", field, max))
	}
	for i, id := range ids {
		if err := validateSegment(id, field); err != nil {
			return err
		}
		if slices.Contains(ids[:i], id) {
			return invalid("invalid_argument", field+" repeats an ID")
		}
	}
	return nil
}

func (m *management) planDeadLetterReplay(ctx context.Context, a planReplayArgs) (any, error) {
	req := replayRequest{All: a.All}
	switch {
	case a.All && len(a.EventIDs) > 0, !a.All && len(a.EventIDs) == 0:
		return nil, invalid("invalid_argument", "give event_ids or all, not both")
	case a.All:
		req.Limit = a.Limit
		if req.Limit == 0 {
			req.Limit = 100
		}
		if req.Limit < 1 || req.Limit > maxReplayAll {
			return nil, invalid("invalid_argument", "limit must be from 1 to 200")
		}
	default:
		if err := validIDs(a.EventIDs, "event_ids", maxReplayIDs); err != nil {
			return nil, err
		}
		req.EventIDs = slices.Clone(a.EventIDs)
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "dlq_replay", req)
	if err != nil {
		return nil, err
	}
	var preview []api.DeadLetterEvent
	previewNote := ""
	if a.All {
		// The list is newest first; a replay-all takes the oldest. Read
		// pages back to the oldest and preview exactly those.
		before := ""
		for range maxReplayPreviewPages {
			page, err := m.client.ListDeadLetterEvents(ctx, a.App, 100, before)
			if err != nil {
				return nil, err
			}
			preview = append(preview, page.Events...)
			before = page.NextBefore
			if before == "" {
				break
			}
		}
		if before != "" {
			previewNote = "More dead letters exist than this preview read; the replay takes older ones than those shown."
		}
		slices.SortStableFunc(preview, func(x, y api.DeadLetterEvent) int { return x.LastFailedAt.Compare(y.LastFailedAt) })
		preview = preview[:min(len(preview), req.Limit)]
	} else {
		for _, id := range req.EventIDs {
			e, err := m.client.GetDeadLetterEvent(ctx, a.App, id)
			if err != nil {
				return nil, err
			}
			if e.AppID != "" && e.AppID != p.AppID {
				return nil, invalid("app_access_denied", "a dead letter belongs to a different app")
			}
			preview = append(preview, e)
		}
	}
	summary := make([]map[string]any, 0, len(preview))
	for _, e := range preview {
		summary = append(summary, map[string]any{"id": e.ID, "source": e.Source, "error_kind": e.ErrorKind, "retry_count": e.RetryCount, "last_failed_at": e.LastFailedAt})
	}
	view := map[string]any{"request": req, "dead_letters": summary}
	if previewNote != "" {
		view["preview_note"] = previewNote
	}
	changes, _ := json.Marshal(view)
	p.Operation.Changes = changes
	p.TrafficEffect = "Replays the dead letters through the app's current code: their side effects (writes, emails, charges) happen again. Fix the cause first."
	return m.savePlan(p)
}

func (m *management) planWorkflowRecovery(ctx context.Context, a planWorkflowRecoveryArgs) (any, error) {
	req := workflowRecoveryRequest{Action: a.Action}
	var before *api.WorkflowRunResponse
	switch a.Action {
	case "retry_step", "resume", "cancel":
		run, err := m.workflowRunOf(ctx, a.App, a.RunID, false)
		if err != nil {
			return nil, err
		}
		before, req.RunID = &run, a.RunID
		if a.Action == "retry_step" {
			if err := validateSegment(a.Step, "step"); err != nil {
				return nil, err
			}
			req.Step = a.Step
		}
		if a.Action == "resume" {
			req.ExpectedResumeCount = run.ResumeCount
		}
	case "cancel_queued":
		if err := validIDs(a.RunIDs, "run_ids", maxCancelQueuedRuns); err != nil {
			return nil, err
		}
		for _, id := range a.RunIDs {
			if _, err := m.workflowRunOf(ctx, a.App, id, false); err != nil {
				return nil, err
			}
		}
		req.RunIDs = slices.Clone(a.RunIDs)
	default:
		return nil, invalid("invalid_argument", "action must be retry_step, resume, cancel or cancel_queued")
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "workflow_recovery", req)
	if err != nil {
		return nil, err
	}
	view := map[string]any{"request": req}
	if before != nil {
		view["run"] = map[string]any{"id": before.ID, "workflow": before.WorkflowName, "status": before.Status, "current_step": before.CurrentStep, "last_error": before.LastError, "resume_count": before.ResumeCount}
	}
	p.Operation.Changes, _ = json.Marshal(view)
	p.TrafficEffect = map[string]string{
		"retry_step":    "Runs the failed step again with its recorded input; the step's side effects can happen again.",
		"resume":        "Resumes the run from where it stopped. Fails instead if someone else resumed it since the plan.",
		"cancel":        "Cancels the run; finished steps are not undone.",
		"cancel_queued": "Cancels runs that have not started; running runs are left alone.",
	}[a.Action]
	return m.savePlan(p)
}

func (m *management) planMonitoringChange(ctx context.Context, a planMonitoringArgs) (any, error) {
	req := monitoringRequest{Kind: a.Kind, Op: a.Op, ID: a.ID}
	if (a.Op != "create") != (a.ID != "") {
		return nil, invalid("invalid_argument", "id is required for enable, disable and update, and refused for create")
	}
	if a.ID != "" {
		if err := validateSegment(a.ID, "id"); err != nil {
			return nil, err
		}
	}
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	switch a.Kind + "/" + a.Op {
	case "slo/create":
		if a.SLO == nil || a.SLO.Name == "" || a.SLO.SLI == "" || a.SLO.ObjectivePct <= 0 || a.SLO.ObjectivePct >= 100 || a.SLO.WindowDays < 1 || a.SLO.WindowDays > 90 {
			return nil, invalid("invalid_argument", "slo needs name, sli, objective_pct between 0 and 100 and window_days from 1 to 90")
		}
		req.SLO = a.SLO
	case "synthetic/create":
		if a.Synthetic == nil || a.Synthetic.Name == "" || !strings.HasPrefix(a.Synthetic.Path, "/") || a.Synthetic.IntervalSeconds < 60 {
			return nil, invalid("invalid_argument", "synthetic needs name, a path starting with / and interval_seconds of at least 60")
		}
		req.Synthetic = a.Synthetic
	case "synthetic/enable", "synthetic/disable":
		check, err := m.client.GetSyntheticCheck(ctx, a.App, a.ID)
		if err != nil {
			return nil, err
		}
		if check.ID != a.ID {
			return nil, invalid("invalid_response", "synthetic check identity differs")
		}
	case "alert/create":
		s := a.Alert
		if s == nil || s.Name == nil || *s.Name == "" || s.Metric == "" || s.Comparison == nil || s.Threshold == nil || s.WindowSpec == nil || s.ChannelIDs == nil || len(*s.ChannelIDs) == 0 {
			return nil, invalid("invalid_argument", "alert needs name, metric, comparison, threshold, window_spec and at least one existing channel_ids entry; agents cannot add webhooks")
		}
		req.Alert = s
	case "alert/update":
		s := a.Alert
		if s == nil || (s.Name == nil && s.Comparison == nil && s.Threshold == nil && s.WindowSpec == nil && s.ChannelIDs == nil && s.CooldownMinutes == nil && s.Enabled == nil) || s.Metric != "" || s.SLOID != "" || s.SyntheticCheckID != "" {
			return nil, invalid("invalid_argument", "alert update changes name, comparison, threshold, window_spec, channel_ids, cooldown_minutes or enabled")
		}
		rule, err := m.client.GetAlertRule(ctx, a.App, a.ID)
		if err != nil {
			return nil, err
		}
		if rule.ID != a.ID || rule.AppID != app.ID {
			return nil, invalid("app_access_denied", "the alert rule belongs to a different app")
		}
		req.Alert = s
	default:
		return nil, invalid("invalid_argument", "change must be slo/create, synthetic/create|enable|disable or alert/create|update; deletes stay with people")
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "monitoring_change", req)
	if err != nil {
		return nil, err
	}
	p.Operation.Changes = p.Operation.Request
	p.TrafficEffect = "Changes monitoring only: no deployment, traffic or app behavior changes. Alerts notify existing channels."
	return m.savePlan(p)
}

// runAsyncOperation applies the batch 2 operation kinds. Each native call
// carries a key derived from the plan, so a retried apply repeats nothing.
func (m *management) runAsyncOperation(ctx context.Context, p *releasePlan) (any, error) {
	base := ctx
	ctx = api.ContextWithIdempotencyKey(base, "mcp-operation-"+p.ID)
	switch p.Operation.Kind {
	case "dlq_replay":
		var req replayRequest
		if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
			return nil, err
		}
		if req.All {
			return m.client.ReplayAllDeadLetterEvents(ctx, p.App, req.Limit)
		}
		replayed := make([]string, 0, len(req.EventIDs))
		for _, id := range req.EventIDs {
			if _, err := m.client.ReplayDeadLetterEvent(api.ContextWithIdempotencyKey(base, "mcp-operation-"+p.ID+":"+id), p.App, id); err != nil {
				return nil, fmt.Errorf("replayed %d of %d before %s failed: %w", len(replayed), len(req.EventIDs), id, err)
			}
			replayed = append(replayed, id)
		}
		return map[string]any{"replayed": replayed}, nil
	case "workflow_recovery":
		var req workflowRecoveryRequest
		if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
			return nil, err
		}
		switch req.Action {
		case "retry_step":
			return m.client.RetryWorkflowStep(ctx, req.RunID, req.Step)
		case "resume":
			count := req.ExpectedResumeCount
			return m.client.ResumeWorkflowRunWithIdempotencyKey(base, req.RunID, api.ResumeWorkflowRunRequest{ExpectedResumeCount: &count}, "mcp-operation-"+p.ID)
		case "cancel":
			return m.client.CancelWorkflowRun(ctx, req.RunID)
		case "cancel_queued":
			return m.client.CancelUnstartedWorkflowRuns(ctx, p.App, api.WorkflowQueuedRunCancelRequest{RunIDs: req.RunIDs})
		}
	case "monitoring_change":
		var req monitoringRequest
		if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
			return nil, err
		}
		switch req.Kind + "/" + req.Op {
		case "slo/create":
			return m.client.CreateSLO(ctx, p.App, *req.SLO)
		case "synthetic/create":
			return m.client.CreateSyntheticCheck(ctx, p.App, *req.Synthetic)
		case "synthetic/enable", "synthetic/disable":
			return m.client.UpdateSyntheticCheck(ctx, p.App, req.ID, api.UpdateSyntheticCheckRequest{Enabled: req.Op == "enable"})
		case "alert/create":
			s := req.Alert
			return m.client.CreateAlertRule(ctx, p.App, api.CreateAlertRuleRequest{Name: *s.Name, Enabled: s.Enabled, Metric: s.Metric, Comparison: *s.Comparison, Threshold: *s.Threshold, WindowSpec: *s.WindowSpec, SLOID: s.SLOID, SyntheticCheckID: s.SyntheticCheckID, ChannelIDs: *s.ChannelIDs, CooldownMinutes: s.CooldownMinutes})
		case "alert/update":
			s := req.Alert
			return m.client.UpdateAlertRule(ctx, p.App, req.ID, api.UpdateAlertRuleRequest{Name: s.Name, Enabled: s.Enabled, Comparison: s.Comparison, Threshold: s.Threshold, WindowSpec: s.WindowSpec, ChannelIDs: s.ChannelIDs, CooldownMinutes: s.CooldownMinutes})
		}
	}
	return nil, invalid("invalid_plan", "unsupported operation kind")
}
