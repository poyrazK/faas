package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const savedDefaultPlanTemplate = "gregale://default/apps/{app}/plans/{plan_id}/review"
const savedNamedPlanTemplate = "gregale://projects/{project}/environments/{environment}/apps/{app}/plans/{plan_id}/review"
const savedEnvironmentPlanTemplate = "gregale://projects/{project}/environments/{environment}/plans/{plan_id}/review"
const savedEnvironmentReviewGuidance = "Both original immutable environments and every project workload require current consent. Environment rollback checks have partial coverage; captured target-release guards remain native. Approval is never inferred from a read."

func registerSavedPlanResources(s *mcp.Server, m *management) {
	for _, entry := range []struct{ uri, name string }{{savedDefaultPlanTemplate, "gregale_default_saved_plan_review"}, {savedNamedPlanTemplate, "gregale_named_saved_plan_review"}, {savedEnvironmentPlanTemplate, "gregale_environment_saved_plan_review"}} {
		description := "Read-only original-plan recovery recipe with exact history/review/check calls. Requires current consent and an unexpired connection-local plan. No fresh baselines, approvals or writes. Cached recipes grant no authority; reread and use guarded tools."
		if entry.uri != savedEnvironmentPlanTemplate {
			description += " Retained deployment, explicit request promotion, service/worker rollout and route-budget attempts can provide outcome recovery after expiry, without review/check calls. Follow any observation call returned by the lookup."
		}
		s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: entry.uri, Name: entry.name, MIMEType: "application/json", Description: description}, m.readSavedPlanResource)
	}
}

func (m *management) validateSavedRecoveryTarget(a getActionArgs, environment bool) error {
	for _, value := range []string{a.App, a.Project, a.Environment, a.PlanID} {
		if m.clean(value) != value {
			return invalid("invalid_argument", "unsafe saved-plan selector")
		}
	}
	if !validActionID(a.PlanID) || !environment && validateSegment(a.App, "app") != nil {
		return invalid("invalid_argument", "provide an exact original plan ID and target")
	}
	if environment || a.Project != "" || a.Environment != "" {
		if validateSegment(a.Project, "project") != nil || !api.ValidProjectEnvironmentSlug(a.Environment) {
			return invalid("invalid_argument", "provide both project and named environment")
		}
	}
	return nil
}

func (m *management) parseSavedPlanResource(uri string) (getActionArgs, bool, error) {
	var args getActionArgs
	u, err := url.Parse(uri)
	if err != nil || len(uri) > 1024 || u.Scheme != "gregale" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.Contains(uri, "%") {
		return args, false, invalid("invalid_resource", "use a canonical saved-plan resource URI")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	environment := false
	switch {
	case u.Host == "default" && len(parts) == 5 && parts[0] == "apps" && parts[2] == "plans" && parts[4] == "review":
		args.App, args.PlanID = parts[1], parts[3]
	case u.Host == "projects" && len(parts) == 8 && parts[1] == "environments" && parts[3] == "apps" && parts[5] == "plans" && parts[7] == "review":
		args.Project, args.Environment, args.App, args.PlanID = parts[0], parts[2], parts[4], parts[6]
	case u.Host == "projects" && len(parts) == 6 && parts[1] == "environments" && parts[3] == "plans" && parts[5] == "review":
		args.Project, args.Environment, args.PlanID = parts[0], parts[2], parts[4]
		environment = true
	default:
		return args, false, invalid("invalid_resource", "unknown saved-plan recovery resource")
	}
	if m.validateSavedRecoveryTarget(args, environment) != nil || uri != savedPlanRecoveryURI(args, environment) {
		return args, environment, invalid("invalid_resource", "invalid saved-plan recovery selector")
	}
	return args, environment, nil
}

func savedPlanRecoveryURI(args getActionArgs, environment bool) string {
	if environment {
		return "gregale://projects/" + args.Project + "/environments/" + args.Environment + "/plans/" + args.PlanID + "/review"
	}
	if args.Environment != "" {
		return "gregale://projects/" + args.Project + "/environments/" + args.Environment + "/apps/" + args.App + "/plans/" + args.PlanID + "/review"
	}
	return "gregale://default/apps/" + args.App + "/plans/" + args.PlanID + "/review"
}

type savedPlanRecovery struct {
	ReviewKind       string              `json:"review_kind"`
	Freshness        string              `json:"freshness"`
	NativeCompletion string              `json:"native_completion"`
	Guidance         string              `json:"guidance"`
	Workflow         savedPromptWorkflow `json:"workflow"`
}

func (m *management) savedPlanRecoveryResource(ctx context.Context, args getActionArgs, environment bool) (*guardedRead, error) {
	guard, calls, err := m.savedPromptWorkflow(ctx, args, environment)
	if err != nil {
		return nil, err
	}
	guidance := savedRecoveryGuidance(calls)
	if environment {
		guidance += " " + savedEnvironmentReviewGuidance
	}
	guard.value = savedPlanRecovery{ReviewKind: "saved_plan_recovery_recipe", Freshness: "not_checked", NativeCompletion: "not_observed", Guidance: guidance, Workflow: calls}
	return guard, nil
}

func (m *management) readSavedPlanResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, m.opts.Timeout)
	defer cancel()
	args, environment, err := m.parseSavedPlanResource(req.Params.URI)
	if err != nil {
		return nil, m.contextError(err)
	}
	guard, err := m.savedPlanRecoveryResource(ctx, args, environment)
	if err != nil {
		return nil, m.contextError(err)
	}
	value, err := checkedResourceValue(ctx, guard)
	if err != nil {
		return nil, m.contextError(err)
	}
	data, err := m.safeOutput(value)
	if err != nil {
		return nil, m.contextError(err)
	}
	result := &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(data.(json.RawMessage))}}}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxResultBytes-maxDiagnosticBytes {
		return nil, m.contextError(invalid("response_too_large", "saved-plan recipe exceeds the resource frame bound"))
	}
	return result, nil
}

func savedPlanResourceCompletionFields(uri string) map[string]bool {
	switch uri {
	case savedDefaultPlanTemplate:
		return map[string]bool{"app": true, "plan_id": true}
	case savedNamedPlanTemplate:
		return map[string]bool{"project": true, "environment": true, "app": true, "plan_id": true}
	case savedEnvironmentPlanTemplate:
		return map[string]bool{"project": true, "environment": true, "plan_id": true}
	}
	return nil
}
