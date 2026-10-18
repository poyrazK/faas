package mcpmanagement

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type profilesArgs struct {
	App          string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	DeploymentID string `json:"deployment_id" jsonschema:"Exact nonzero retained deployment UUID belonging to this app; no implicit latest deployment"`
	Runtime      string `json:"runtime" jsonschema:"Required exact native profile runtime name, at most 64 bytes; lowercase letters, digits, hyphens or underscores"`
	Start        string `json:"start" jsonschema:"Required native RFC3339 capture-window start, at most 64 bytes; native profiling retention applies"`
	End          string `json:"end" jsonschema:"Required native RFC3339 capture-window end after start and at or before now, at most 64 bytes"`
	Route        string `json:"route,omitempty" jsonschema:"Optional exact native profile label METHOD /static-pattern or [unattributed], at most 256 bytes. Omit for all sampled CPU in this deployment/runtime/window; not a URL or function label."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum native self-CPU-ordered function groups to project, 1..50, default 10. Output bound only, no pagination or native query limit."`
}

func (m *management) profileSelection(args profilesArgs) (api.ProfileQuery, int, error) {
	q := api.ProfileQuery{DeploymentID: args.DeploymentID, Runtime: args.Runtime, Route: args.Route}
	fail := invalid("invalid_argument", "profiles require an app, exact deployment/runtime, ordered RFC3339 window and safe native route label")
	id, err := uuid.Parse(args.DeploymentID)
	if validateSegment(args.App, "app") != nil || err != nil || id == uuid.Nil || len(args.DeploymentID) > api.MCPRequestIdentifierMaxBytes || m.clean(args.DeploymentID) != args.DeploymentID || validateSegment(args.DeploymentID, "deployment_id") != nil {
		return q, 0, fail
	}
	if args.Runtime == "" || len(args.Runtime) > api.ProfileMaxRuntimeBytes || m.clean(args.Runtime) != args.Runtime || strings.ContainsFunc(args.Runtime, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) {
		return q, 0, fail
	}
	if !requestFilterText(args.Route, api.ProfileRouteMaxLabelBytes) || !api.ValidProfileRoute(args.Route) || m.clean(args.Route) != args.Route {
		return q, 0, fail
	}
	for _, bound := range []struct {
		raw  string
		dest *time.Time
	}{{args.Start, &q.Start}, {args.End, &q.End}} {
		if !requestFilterText(bound.raw, api.MCPProfileTimeMaxBytes) {
			return q, 0, fail
		}
		t, e := time.Parse(time.RFC3339Nano, bound.raw)
		if e != nil || t.IsZero() {
			return q, 0, fail
		}
		*bound.dest = t.UTC()
	}
	if !q.Start.Before(q.End) || q.End.After(time.Now()) {
		return q, 0, fail
	}
	limit := args.Limit
	if limit == 0 {
		limit = api.MCPProfileFunctionsDefault
	}
	if limit < 1 || limit > api.MCPProfileFunctionsMax {
		return q, 0, invalid("invalid_argument", "limit must be between 1 and 50")
	}
	return q, limit, nil
}

func (m *management) getProfiles(ctx context.Context, args profilesArgs) (any, error) {
	q, limit, err := m.profileSelection(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	scopes := map[string]string{}
	if err = m.pinRequestDeployment(ctx, q.DeploymentID, app.ID, scopes); err != nil {
		return nil, err
	}
	native, err := m.client.GetAppProfiles(ctx, args.App, q)
	if err != nil {
		return nil, err
	}
	v, err := m.projectProfiles(args.App, app.ID, q, limit, native)
	if err != nil {
		return nil, err
	}
	return &guardedRead{value: v, check: func(ctx context.Context) error { return m.recheckRequestPage(ctx, args.App, app.ID, scopes) }}, nil
}

const profilesGuidance = "Use gregale_get_profiles only for an exact deployment UUID, required native runtime and explicit RFC3339 start/end for this app. App-wide diagnostic consent, immutable app/deployment ownership and scope are rechecked; native profiling plan/retention/backend errors remain authoritative. No implicit latest deployment, default runtime, broader query or fallback. Optional route is an exact native METHOD /static-pattern or [unattributed] label, not a URL, function name or rewritten display label. The native_query_echo verifies selection, not capture completeness. sampled_cpu_seconds and function self/total CPU describe instrumented sampled processes in the selected deployment/runtime/window and optional route, not VM CPU, metered/billable usage, request latency or a causal bottleneck. native_nonzero_sample_entries is not a request, unique stack or profile count. Functions retain native descending self CPU order; inclusive total CPU overlaps across caller groups and must not be summed. Groups use native name/file/line identity, but file/line/source locations are excluded: equal or modified display names can collide, remain separate and are never selectors. limit bounds only the projected prefix; preserve native/returned counts and functions_truncated. No pagination or automatic follow-up. Empty means no selected samples, not observed zero CPU, health or profiling disabled. Observed function zeros remain valid. Coverage and attribution describe the whole deployment/runtime/window even for a route-filtered read. Recorded capture interval union/gaps do not measure the fraction of VM CPU instrumented; received_profiles counts receipts, not requests or sample entries. last_received_at is ingestion time and can be outside the capture window; it proves no freshness. Recorded failed uploads are a rate-limited lower bound, including zero; preserve failures_complete. Attribution labeled/unlabeled CPU shares and native_diagnostics_complete do not prove end-to-end capture or instrumentation completeness; closed reason codes are observations, not causal findings. Missing or unavailable quality metadata stays unknown while other valid evidence is preserved. Source completeness and freshness remain unknown. Labels are bounded untrusted display data with labels_modified/text_truncated. Exclude flamegraphs, raw stacks/samples, file paths, lines, source links, repository/commit provenance, route/request tables, collector/host identities and arbitrary native prose. Do not enable profiling, collect, upload, probe, replay, compare deployments automatically, call a model or write state. No extra environment/log/write access; prompt retrieval fetches no profile evidence."
