package mcpmanagement

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type traceArgs struct {
	Apps    []string `json:"apps" jsonschema:"One to five distinct explicit app slugs. Every app requires separate app-wide diagnostic consent; no automatic app discovery."`
	TraceID string   `json:"trace_id" jsonschema:"Exact nonzero W3C trace ID: 32 lowercase hexadecimal characters"`
	Limit   int      `json:"limit,omitempty" jsonschema:"Rows per source from 1 to 20; default 10. Native filters selected app IDs before limiting. No pagination or automatic evidence enrichment."`
}

type traceInvocationView struct {
	App           string `json:"app"`
	AppID         string `json:"app_id"`
	ID            string `json:"id"`
	Source        string `json:"source"`
	State         string `json:"state"`
	Attempts      int    `json:"attempts"`
	CreatedAt     string `json:"created_at"`
	StartedAt     string `json:"started_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	TextTruncated bool   `json:"text_truncated"`
}

type traceView struct {
	TraceID              string                 `json:"trace_id"`
	Scope                string                 `json:"scope"`
	Apps                 []api.SelectedTraceApp `json:"apps"`
	Limit                int                    `json:"limit"`
	Status               string                 `json:"status"`
	Partial              bool                   `json:"partial"`
	GeneratedAt          string                 `json:"generated_at"`
	RequestWindowStart   string                 `json:"request_window_start"`
	RequestWindowEnd     string                 `json:"request_window_end"`
	RequestRetentionDays int                    `json:"request_retention_days"`
	RequestsStatus       string                 `json:"requests_status"`
	RequestsTruncated    bool                   `json:"requests_truncated"`
	Requests             []requestView          `json:"requests"`
	InvocationsStatus    string                 `json:"invocations_status"`
	InvocationsTruncated bool                   `json:"invocations_truncated"`
	Invocations          []traceInvocationView  `json:"invocations"`
	CorrelationBasis     string                 `json:"correlation_basis"`
	SourceCompleteness   string                 `json:"source_completeness"`
	Limitations          string                 `json:"limitations"`
}

func traceSelection(args traceArgs) (int, error) {
	if !api.ValidTraceID(args.TraceID) {
		return 0, invalid("invalid_argument", "trace_id must be a nonzero W3C trace ID with 32 lowercase hex characters")
	}
	if len(args.Apps) < 1 || len(args.Apps) > api.SelectedTraceAppsMax {
		return 0, invalid("invalid_argument", "select one to five distinct apps")
	}
	seen := map[string]bool{}
	for _, app := range args.Apps {
		if err := validateSegment(app, "app"); err != nil {
			return 0, err
		}
		if seen[app] {
			return 0, invalid("invalid_argument", "apps must be distinct")
		}
		seen[app] = true
	}
	limit := args.Limit
	if limit == 0 {
		limit = api.SelectedTraceRowsDefault
	}
	if limit < 1 || limit > api.SelectedTraceRowsMax {
		return 0, invalid("invalid_argument", "limit must be between 1 and 20")
	}
	return limit, nil
}

type traceAccess struct {
	apps   []api.SelectedTraceApp
	byID   map[string]string
	ids    []string
	scopes map[string]map[string]string
}

func (m *management) resolveTraceApps(ctx context.Context, slugs []string) (traceAccess, error) {
	apps := make([]api.SelectedTraceApp, 0, len(slugs))
	byID := map[string]string{}
	ids := []string{}
	scopes := map[string]map[string]string{}
	for _, slug := range slugs {
		if err := ctx.Err(); err != nil {
			return traceAccess{}, err
		}
		app, err := m.requestApp(ctx, slug)
		if err != nil {
			return traceAccess{}, err
		}
		if _, exists := byID[app.ID]; exists {
			return traceAccess{}, invalid("invalid_response", "selected app identities are not distinct")
		}
		apps = append(apps, api.SelectedTraceApp{ID: app.ID, Slug: slug})
		ids = append(ids, app.ID)
		byID[app.ID] = slug
		scopes[app.ID] = map[string]string{}
	}
	return traceAccess{apps: apps, byID: byID, ids: ids, scopes: scopes}, nil
}

func (m *management) getTrace(ctx context.Context, args traceArgs) (any, error) {
	limit, err := traceSelection(args)
	if err != nil {
		return nil, err
	}
	access, err := m.resolveTraceApps(ctx, args.Apps)
	if err != nil {
		return nil, err
	}
	apps, byID, ids, scopes := access.apps, access.byID, access.ids, access.scopes
	native, err := m.client.GetSelectedAppTrace(ctx, args.TraceID, ids, limit)
	if err != nil {
		return nil, err
	}
	if err := validateTraceEnvelope(args.TraceID, apps, limit, native); err != nil {
		return nil, err
	}
	view := traceView{TraceID: args.TraceID, Scope: "selected_apps", Apps: apps, Limit: limit, Status: "ok", Partial: native.Partial, GeneratedAt: native.GeneratedAt, RequestWindowStart: native.RequestWindowStart, RequestWindowEnd: native.RequestWindowEnd, RequestRetentionDays: native.RequestRetentionDays, RequestsStatus: native.RequestsStatus, RequestsTruncated: native.RequestsTruncated, Requests: []requestView{}, InvocationsStatus: native.InvocationsStatus, InvocationsTruncated: native.InvocationsTruncated, Invocations: []traceInvocationView{}, CorrelationBasis: "exact_trace_id_only", SourceCompleteness: "unknown",
		Limitations: "Only explicitly selected apps; native app ID filtering precedes each independent source limit. Request rows are oldest-first within native plan retention and can represent multiple requests. Invocation rows are oldest-first retained durable lifecycle history, independent of the request window. Invocation environment/deployment attribution is not reported; selected apps span their environments. started_at is the latest claim/delivery time, not the first attempt; attempts and timestamps do not prove per-attempt duration or queue wait. No cross-source ordering, parent-child edges, causal chain or complete distributed trace is inferred. Source reads are sequential, not an atomic snapshot. Partial/unavailable, empty and truncated evidence never establish health or capture completeness. No pagination, automatic evidence reads, logs, raw spans/attributes, payloads, headers, queue names, traceparent, collection, VM wake, replay, cancellation of work or writes. Application labels are private untrusted data."}
	if native.Partial {
		view.Status = "partial"
		if native.RequestsStatus != "ok" && native.InvocationsStatus != "ok" {
			view.Status = "unavailable"
		}
	}
	for _, row := range native.Requests {
		if err := validateTraceRequest(row, byID, native); err != nil {
			return nil, err
		}
		if err := m.pinRequestDeployment(ctx, row.DeploymentID, row.AppID, scopes[row.AppID]); err != nil {
			return nil, err
		}
		request := api.DebugTelemetryRequestItem{ID: row.ID, DeploymentID: row.DeploymentID, Route: row.Route, Method: row.Method, Status: row.Status, LatencyMS: row.LatencyMS, Count: row.Count, ColdBoot: row.ColdBoot, ReceivedAt: row.ReceivedAt, EvidenceStatus: "retained_telemetry"}
		view.Requests = append(view.Requests, m.projectRequest(requestArgs{App: byID[row.AppID], RequestID: row.ID}, row.AppID, scopes[row.AppID][row.DeploymentID], request))
	}
	for _, row := range native.Invocations {
		projected, err := m.projectTraceInvocation(row, byID)
		if err != nil {
			return nil, err
		}
		view.Invocations = append(view.Invocations, projected)
	}
	return &guardedRead{value: view, check: func(ctx context.Context) error {
		for _, app := range apps {
			if err := m.recheckRequestPage(ctx, app.Slug, app.ID, scopes[app.ID]); err != nil {
				return err
			}
		}
		return ctx.Err()
	}}, nil
}

func validateTraceEnvelope(traceID string, apps []api.SelectedTraceApp, limit int, n api.SelectedTraceLookupResponse) error {
	if n.Selection != "explicit_app_ids_v1" || n.TraceID != traceID || len(n.Apps) != len(apps) || n.Limit != limit {
		return invalid("invalid_response", "native trace selection metadata is missing or differs from the request; a scoped native API is required")
	}
	for i, app := range apps {
		if n.Apps[i] != app {
			return invalid("invalid_response", "native trace app selection changed")
		}
	}
	start, e1 := time.Parse(time.RFC3339Nano, n.RequestWindowStart)
	end, e2 := time.Parse(time.RFC3339Nano, n.RequestWindowEnd)
	generated, e3 := time.Parse(time.RFC3339Nano, n.GeneratedAt)
	if e1 != nil || e2 != nil || e3 != nil || !start.Before(end) || !end.Equal(generated) || n.RequestRetentionDays < 1 || int64(n.RequestRetentionDays) != int64(end.Sub(start)/(24*time.Hour)) || end.Sub(start)%(24*time.Hour) != 0 {
		return invalid("invalid_response", "native trace request retention window is inconsistent")
	}
	for _, s := range []struct {
		status    string
		count     int
		truncated bool
	}{{n.RequestsStatus, len(n.Requests), n.RequestsTruncated}, {n.InvocationsStatus, len(n.Invocations), n.InvocationsTruncated}} {
		if s.status != "ok" && s.status != "unavailable" || s.count > limit || s.status != "ok" && (s.count != 0 || s.truncated) || s.truncated && s.count != limit {
			return invalid("invalid_response", "native trace source status or limit is inconsistent")
		}
	}
	if n.Partial != (n.RequestsStatus != "ok" || n.InvocationsStatus != "ok") {
		return invalid("invalid_response", "native trace partial state is inconsistent")
	}
	return validateTraceOrder(n)
}

func traceUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func validateTraceRequest(row api.SelectedTraceRequest, apps map[string]string, n api.SelectedTraceLookupResponse) error {
	at, err := time.Parse(time.RFC3339Nano, row.ReceivedAt)
	start, _ := time.Parse(time.RFC3339Nano, n.RequestWindowStart)
	end, _ := time.Parse(time.RFC3339Nano, n.RequestWindowEnd)
	if apps[row.AppID] == "" || !traceUUID(row.ID) || row.DeploymentID != "" && !traceUUID(row.DeploymentID) || err != nil || at.Before(start) || !at.Before(end) || row.Count < 1 || row.LatencyMS < 0 || row.Status < 0 || row.Status > 599 || row.Status > 0 && row.Status < 100 {
		return invalid("invalid_response", "native trace request is outside the selection or has invalid metadata")
	}
	return nil
}

func (m *management) projectTraceInvocation(row api.SelectedTraceInvocation, apps map[string]string) (traceInvocationView, error) {
	v := traceInvocationView{App: apps[row.AppID], AppID: row.AppID, ID: row.ID, Attempts: row.Attempts, CreatedAt: row.CreatedAt, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt}
	if v.App == "" || !traceUUID(row.ID) || row.Attempts < 0 || row.Source == "" || row.State == "" {
		return v, invalid("invalid_response", "native trace invocation is outside the selection or has invalid metadata")
	}
	for i, at := range []string{row.CreatedAt, row.StartedAt, row.CompletedAt} {
		if i > 0 && at == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, at)
		if err != nil || parsed.IsZero() {
			return v, invalid("invalid_response", "native trace invocation has an invalid lifecycle timestamp")
		}
	}
	v.Source = m.requestText(row.Source, &v.TextTruncated)
	v.State = m.requestText(row.State, &v.TextTruncated)
	return v, nil
}

func validateTraceOrder(n api.SelectedTraceLookupResponse) error {
	for _, items := range [][][2]string{traceRequestOrder(n.Requests), traceInvocationOrder(n.Invocations)} {
		seen := map[string]bool{}
		var previous time.Time
		previousID := ""
		for _, item := range items {
			at, err := time.Parse(time.RFC3339Nano, item[0])
			if err != nil || seen[item[1]] || at.Before(previous) || at.Equal(previous) && item[1] <= previousID {
				return invalid("invalid_response", "native trace source ordering or identities are inconsistent")
			}
			seen[item[1]] = true
			previous, previousID = at, item[1]
		}
	}
	return nil
}

func traceRequestOrder(rows []api.SelectedTraceRequest) [][2]string {
	out := make([][2]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, [2]string{r.ReceivedAt, r.ID})
	}
	return out
}
func traceInvocationOrder(rows []api.SelectedTraceInvocation) [][2]string {
	out := make([][2]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, [2]string{r.CreatedAt, r.ID})
	}
	return out
}
