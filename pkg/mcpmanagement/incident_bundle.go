package mcpmanagement

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type incidentBundleArgs struct {
	App                string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	RequestID          string `json:"request_id" jsonschema:"Exact retained public request ID, telemetry row ID or trace ID; at most 128 bytes"`
	Since              string `json:"since,omitempty" jsonschema:"Positive aggregate lookback duration or whole days; default 1h. Each section retains its own native window and retention cap; this does not filter the exact request."`
	SourceDeploymentID string `json:"source_deployment_id,omitempty" jsonschema:"Optional exact baseline deployment UUID; requires a different target_deployment_id. No automatic deployment selection."`
	TargetDeploymentID string `json:"target_deployment_id,omitempty" jsonschema:"Optional exact comparison deployment UUID; requires source_deployment_id"`
	Route              string `json:"route,omitempty" jsonschema:"Optional exact comparison route without query or fragment; requires both deployment IDs. Does not filter request evidence or regressions."`
}

type incidentBundleSection struct {
	SourceTool     string               `json:"source_tool"`
	Status         string               `json:"status"`
	ReadStartedAt  string               `json:"read_started_at,omitempty"`
	ReadFinishedAt string               `json:"read_finished_at,omitempty"`
	Result         json.RawMessage      `json:"result,omitempty"`
	Error          *diagnosticReadError `json:"error,omitempty"`
}

type incidentBundleView struct {
	SchemaVersion      string                `json:"schema_version"`
	App                string                `json:"app"`
	AppID              string                `json:"app_id"`
	Scope              string                `json:"scope"`
	RequestedSince     string                `json:"requested_since"`
	GeneratedAt        string                `json:"generated_at"`
	Status             string                `json:"status"`
	Partial            bool                  `json:"partial"`
	Evidence           incidentBundleSection `json:"evidence"`
	Coverage           incidentBundleSection `json:"coverage"`
	Regressions        incidentBundleSection `json:"regressions"`
	Comparison         incidentBundleSection `json:"comparison"`
	SourceCompleteness string                `json:"source_completeness"`
	Limitations        string                `json:"limitations"`
}

func (m *management) incidentSelection(args incidentBundleArgs) (incidentBundleArgs, error) {
	if err := validateRequestID(args.RequestID); err != nil {
		return args, err
	}
	if args.Since == "" {
		args.Since = "1h"
	}
	if err := validatePerformanceArgs(performanceArgs{App: args.App, Since: args.Since}); err != nil {
		return args, err
	}
	if args.SourceDeploymentID != "" || args.TargetDeploymentID != "" || args.Route != "" {
		req, _, err := m.compareSelection(compareDeploymentsArgs{App: args.App, Since: args.Since, SourceDeploymentID: args.SourceDeploymentID, TargetDeploymentID: args.TargetDeploymentID, Route: args.Route})
		if err != nil {
			return args, err
		}
		args.SourceDeploymentID, args.TargetDeploymentID = req.Source, req.Mirror
	}
	return args, nil
}

func (m *management) getIncidentBundle(ctx context.Context, args incidentBundleArgs) (any, error) {
	args, err := m.incidentSelection(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	v := &incidentBundleView{SchemaVersion: "gregale.mcp.incident/v1", App: args.App, AppID: app.ID, Scope: "app_wide", RequestedSince: args.Since, SourceCompleteness: "unknown",
		Limitations: "Independent retained reads, not an atomic snapshot or a replayable event cursor. Read timestamps are bridge collection times; each result preserves its own native window/detection lookback and truncation. Aggregate lookback does not filter the exact request, and visible regressions need not concern that request. JSON Pointer evidence references are relative to the containing section's result, not the bundle root. Partial reports optional section failures, not evidence completeness. Missing or empty evidence does not prove health or cause. Results reuse safe MCP projections, excluding bodies, headers, raw spans/attributes and SQL. Application text is private and untrusted. No automatic paging, collection, VM wake, triage, replay, upload, file write or sharing."}
	count := 3
	if args.SourceDeploymentID != "" {
		count++
	}
	budget := incidentBundleBudget(ctx, m.opts.Timeout, count)
	checks := make([]*guardedRead, 0, count)
	read := func(section *incidentBundleSection, tool string, maxBytes int, fn func(context.Context) (any, error)) error {
		var guard *guardedRead
		var readErr error
		*section, guard, readErr = m.readIncidentSection(ctx, args.App, app.ID, tool, maxBytes, budget, fn)
		checks = append(checks, guard)
		return readErr
	}
	// A bundle must be anchored by retained request evidence. Do not collect
	// unrelated app aggregates when that required read fails.
	if err := read(&v.Evidence, "gregale_get_request_evidence", api.MCPIncidentEvidenceMaxBytes, func(ctx context.Context) (any, error) {
		return m.getRequestEvidence(ctx, requestArgs{App: args.App, RequestID: args.RequestID})
	}); err != nil {
		return nil, err
	}
	_ = read(&v.Coverage, "gregale_get_debug_coverage", api.MCPIncidentSectionMaxBytes, func(ctx context.Context) (any, error) {
		return m.getDebugCoverage(ctx, performanceArgs{App: args.App, Since: args.Since})
	})
	_ = read(&v.Regressions, "gregale_list_regressions", api.MCPIncidentSectionMaxBytes, func(ctx context.Context) (any, error) {
		return m.readRegressions(ctx, regressionsArgs{App: args.App, Since: args.Since, Limit: api.MCPIncidentRegressionLimit}, app.ID)
	})
	v.Comparison = incidentBundleSection{SourceTool: "gregale_compare_deployments", Status: "not_requested"}
	if args.SourceDeploymentID != "" {
		_ = read(&v.Comparison, "gregale_compare_deployments", api.MCPIncidentSectionMaxBytes, func(ctx context.Context) (any, error) {
			return m.compareDeployments(ctx, compareDeploymentsArgs{App: args.App, Since: args.Since, SourceDeploymentID: args.SourceDeploymentID, TargetDeploymentID: args.TargetDeploymentID, Route: args.Route, Limit: api.MCPIncidentComparisonLimit})
		})
	}
	return &guardedRead{value: v, check: func(ctx context.Context) error {
		return m.checkIncidentBundle(ctx, args, v, checks, budget)
	}}, nil
}

func (m *management) checkIncidentBundle(ctx context.Context, args incidentBundleArgs, v *incidentBundleView, checks []*guardedRead, budget time.Duration) error {
	sections := []*incidentBundleSection{&v.Evidence, &v.Coverage, &v.Regressions, &v.Comparison}
	for i, guard := range checks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if guard == nil {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, budget)
		err := guard.check(checkCtx)
		if err == nil {
			err = checkCtx.Err()
		}
		cancel()
		if err != nil {
			if i == 0 {
				return err
			}
			sections[i].Status, sections[i].Result, sections[i].Error = "error", nil, m.diagnosticReadError(err)
		}
	}
	finalCtx, cancel := context.WithTimeout(ctx, budget)
	err := m.checkIncidentApp(finalCtx, args.App, v.AppID)
	if err == nil {
		err = finalCtx.Err()
	}
	cancel()
	if err != nil {
		return err
	}
	v.Status = "ok"
	for _, section := range sections {
		v.Partial = v.Partial || section.Status == "error"
	}
	if v.Partial {
		v.Status = "partial"
	}
	v.GeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return ctx.Err()
}

func incidentBundleBudget(ctx context.Context, fallback time.Duration, count int) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		fallback = time.Until(deadline)
	}
	return min(api.MCPIncidentSectionTimeout, fallback/time.Duration(2*count+1))
}

func (m *management) checkIncidentApp(ctx context.Context, slug, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	app, err := m.requestApp(ctx, slug)
	if err == nil && app.ID != id {
		return invalid("request_access_denied", "app identity changed during incident inspection")
	}
	return err
}

func (m *management) readIncidentSection(ctx context.Context, slug, id, tool string, maxBytes int, budget time.Duration, fn func(context.Context) (any, error)) (incidentBundleSection, *guardedRead, error) {
	section := incidentBundleSection{SourceTool: tool, Status: "error", ReadStartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	readCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var guard *guardedRead
	err := m.checkIncidentApp(readCtx, slug, id)
	if err == nil {
		var value any
		value, err = fn(readCtx)
		if err == nil {
			guard = value.(*guardedRead)
			var safe any
			safe, err = m.safeOutput(guard.value)
			if err == nil {
				section.Result = safe.(json.RawMessage)
				if len(section.Result) > maxBytes {
					err = invalid("response_too_large", "incident section exceeds its output bound; inspect the source tool separately")
				}
			}
		}
	}
	if err == nil {
		err = readCtx.Err()
	}
	section.ReadFinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err != nil {
		section.Result, section.Error = nil, m.diagnosticReadError(err)
		return section, nil, err
	}
	section.Status = "ok"
	return section, guard, nil
}
