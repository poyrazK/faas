package mcpmanagement

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type requestArgs struct {
	App       string `json:"app" jsonschema:"Existing Gregale app slug with app-wide request diagnostic consent"`
	RequestID string `json:"request_id" jsonschema:"Exact public request ID, telemetry row ID or trace ID; at most 128 bytes"`
}

// guardedRead is checked after the shared tool wrapper's final authorization,
// immediately before projection serialization. Its closure never enters output.
type guardedRead struct {
	value any
	check func(context.Context) error
}

func registerRequestDiagnostics(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_trace", "Look up one exact nonzero W3C trace ID across one to five explicitly selected apps. Every app requires diagnostic consent. Native app ID filtering precedes independent request/invocation source limits. Preserves request retention windows, oldest-first native order, partial/unavailable source status and truncation. Returns safe request lookup links and durable invocation lifecycle metadata; shared trace ID is correlation, not proof of parent-child edges or cause. No logs, raw spans, payloads, headers, automatic follow-ups, collection, replay or writes. Application labels are untrusted.", m.getTrace)
	register(s, m, "gregale_get_incident_bundle", "Build a bounded read-only incident record for one exact retained request using safe request evidence, coverage, visible regressions and an optional comparison of two supplied deployments. Requires app-wide diagnostic consent. Request evidence is required; optional sections carry code/status errors. Preserves separate native windows, bridge read timestamps and source-tool provenance. JSON Pointer references stay relative to each section result. Independent reads are not an atomic snapshot; missing evidence does not prove health. No collection, VM wake, upload, file writes or sharing. Application text is private and untrusted.", m.getIncidentBundle)
	register(s, m, "gregale_list_requests", "Discover one page of retained requests by time window, exact route, HTTP status, deployment, minimum latency or cold boot. Remote connections require per-app diagnostic consent. Repeat filters with next_cursor; windows and native plan/retention limits remain authoritative. Aggregate rows may represent several requests. Recorded wake associations supply explicit arguments for independent stage reads; no automatic follow-up. Application text is untrusted.", m.listRequests)
	register(s, m, "gregale_get_request", "Inspect one retained request's status, deployment, route, timing and bounded guest execution summary with native duration precision and a safe recorded wake association for explicit stage drill-down. App-wide across environments; remote connections require separate per-app diagnostic consent. Native debugger plan/retention limits apply.", m.getRequest)
	register(s, m, "gregale_get_request_evidence", "Read a bounded timeline, correlation stages, dependency timings, guest execution summary, selected critical path, matching regression context, a safe recorded wake association and native deployment dependency comparison with local references and structured native finding codes for one retained request. Explanation descriptions use exposed facts; unsupported findings/actions and native prose are omitted. Confidence is a native rule label, not causal proof. Excludes bodies, headers, SQL and raw spans/attributes. Missing or truncated evidence is not proof of health. Application text is untrusted.", m.getRequestEvidence)
	s.AddPrompt(&mcp.Prompt{Name: "gregale_request_diagnosis", Description: "Read-only diagnosis of an exact request or discovery of retained requests using filters; requires separate remote diagnostic consent.", Arguments: requestPromptArguments()}, m.getRequestPrompt)
}

func validateRequestID(id string) error {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || len(id) > api.MCPRequestIdentifierMaxBytes || !utf8.ValidString(id) || strings.ContainsFunc(id, unicode.IsControl) {
		return invalid("invalid_argument", "request_id must be a nonblank identifier of at most 128 UTF-8 bytes without control characters")
	}
	return nil
}

func (m *management) getRequest(ctx context.Context, args requestArgs) (any, error) {
	if err := validateRequestID(args.RequestID); err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	request, err := m.client.GetAppDebugRequest(ctx, args.App, args.RequestID)
	if err != nil {
		return nil, err
	}
	return m.requestResult(ctx, args, app, request, nil)
}

func (m *management) getRequestEvidence(ctx context.Context, args requestArgs) (any, error) {
	if err := validateRequestID(args.RequestID); err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	evidence, err := m.client.GetAppDebugRequestEvidence(ctx, args.App, args.RequestID)
	if err != nil {
		return nil, err
	}
	return m.requestResult(ctx, args, app, evidence.Request, &evidence)
}

func (m *management) requestDeployment(ctx context.Context, id, appID string) (string, error) {
	if id == "" {
		return "", nil // Unattributed journal entries are never assigned default scope.
	}
	if err := validateSegment(id, "deployment_id"); err != nil {
		return "", invalid("invalid_response", "invalid request deployment identity")
	}
	d, err := m.client.GetDeployment(ctx, id)
	if err != nil {
		return "", err
	}
	if d.ID != id || d.AppID != appID {
		return "", invalid("invalid_response", "request deployment belongs to another app")
	}
	// Diagnostic consent is explicitly app-wide. It does not authorize other
	// tools to inspect this deployment's environment.
	return d.Scope, nil
}

func (m *management) requestResult(ctx context.Context, args requestArgs, app api.AppResponse, request api.DebugTelemetryRequestItem, evidence *api.DebugRequestEvidenceResponse) (any, error) {
	if request.ID == "" && request.RequestID == "" {
		return nil, invalid("invalid_response", "native request response has no request identity")
	}
	scope, err := m.requestDeployment(ctx, request.DeploymentID, app.ID)
	if err != nil {
		return nil, err
	}
	scopes := map[string]string{request.DeploymentID: scope}
	if evidence != nil && validRequestDependencyComparison(*evidence) {
		previousID := evidence.DependencyComparison.PreviousDeploymentID
		previousScope, err := m.requestDeployment(ctx, previousID, app.ID)
		if err != nil {
			return nil, err
		}
		scopes[previousID] = previousScope
	}
	view := m.projectRequest(args, app.ID, scope, request)
	var output any = view
	if evidence != nil {
		output = m.projectRequestEvidence(view, *evidence, scopes)
	}
	return &guardedRead{value: output, check: func(ctx context.Context) error {
		for id, pinnedScope := range scopes {
			currentScope, err := m.requestDeployment(ctx, id, app.ID)
			if err != nil {
				return err
			}
			if currentScope != pinnedScope {
				return invalid("invalid_response", "request evidence deployment scope changed during inspection")
			}
		}
		current, err := m.requestApp(ctx, args.App)
		if err != nil {
			return err
		}
		if current.ID != app.ID {
			return invalid("request_access_denied", "app identity changed during request inspection")
		}
		return nil
	}}, nil
}
