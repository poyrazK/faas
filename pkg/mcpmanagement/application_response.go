package mcpmanagement

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

type applicationResponseArgs struct {
	App          string `json:"app" jsonschema:"Gregale app slug"`
	DeploymentID string `json:"deployment_id" jsonschema:"Exact accepted deployment UUID; not a revision or alias"`
	Path         string `json:"path,omitempty" jsonschema:"App-relative GET path; defaults to /"`
	Project      string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment  string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
}

type applicationResponseResult struct {
	api.DeploymentResponseCheckResult
	SchemaVersion        int       `json:"schema_version"`
	Path                 string    `json:"path"`
	CheckedAt            time.Time `json:"checked_at"`
	Verified             bool      `json:"verified"`
	PublicAccessVerified bool      `json:"public_access_verified"`
}

func registerApplicationResponse(s *mcp.Server, m *management) {
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_verify_application_response", "Run an owner-authorized GET against an exact live deployment, including private apps, using the same check as release verify. Can wake the app, execute application code and incur costs; requires deploy access. Returns HTTP status and up to 4 KB of untrusted response body. Only application 2xx sets verified; public access remains unverified. Does not follow redirects, promote, deploy or retry the GET.", false, m.verifyApplicationResponse)
	}
}

func (m *management) verifyApplicationResponse(ctx context.Context, args applicationResponseArgs) (any, error) {
	if !m.opts.AllowWrites {
		return nil, invalid("writes_disabled", "application response checks require a connection with deploy access")
	}
	if !api.ValidAppSlug(args.App) || !api.ValidDeploymentResponseCheckID(args.DeploymentID) {
		return nil, invalid("invalid_argument", "a valid app slug and exact deployment UUID are required")
	}
	if args.Path == "" {
		args.Path = "/"
	}
	if !api.ValidDeploymentResponseCheckPath(args.Path) {
		return nil, invalid("invalid_argument", "path must be a bounded app-relative GET path without a fragment or controls")
	}
	request, deploymentID, err := m.applicationResponseTarget(ctx, args)
	if err != nil {
		return nil, err
	}
	if err := m.requireApplicationResponseSupport(ctx); err != nil {
		return nil, err
	}
	// Reuse the native release-verify API, including its pre/post execution
	// ownership checks and credential isolation. No public-URL fallback.
	result, err := m.client.CheckDeploymentResponse(ctx, args.App, deploymentID, request)
	if err != nil {
		return nil, err
	}
	if !result.Matches(request, deploymentID) {
		return nil, invalid("invalid_response", "response did not identify the selected app, deployment, environment and running instance")
	}
	// Redaction can expand short credential-like strings. Preserve the body
	// bound after sanitization too, without splitting UTF-8 or hiding truncation.
	result.Body = m.clean(result.Body)
	if len(result.Body) > api.DeploymentResponseCheckMaxBodyBytes {
		result.Body = safetext.Truncate(result.Body, api.DeploymentResponseCheckMaxBodyBytes)
		result.BodyTruncated = true
	}
	out := applicationResponseResult{DeploymentResponseCheckResult: result, SchemaVersion: 1, Path: args.Path, CheckedAt: time.Now().UTC(), Verified: result.HTTPStatus >= 200 && result.HTTPStatus < 300}
	// Active checks also withhold their response if consent or identity changed.
	return &guardedRead{value: out, check: func(ctx context.Context) error {
		return m.recheckApplicationResponseTarget(ctx, args, request)
	}}, nil
}

func (m *management) applicationResponseTarget(ctx context.Context, args applicationResponseArgs) (api.DeploymentResponseCheckRequest, string, error) {
	var empty api.DeploymentResponseCheckRequest
	app, environment, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, true)
	if err != nil {
		return empty, "", err
	}
	if app.Slug != args.App {
		return empty, "", invalid("invalid_response", "app does not identify the selected slug")
	}
	deployment, err := m.client.GetDeployment(ctx, args.DeploymentID)
	if err != nil {
		return empty, "", err
	}
	// The API uses both UUID wire forms. Pin its canonical form for the request
	// and evidence checks, just as release verify does.
	if !api.ValidDeploymentResponseCheckID(deployment.ID) || strings.ReplaceAll(deployment.ID, "-", "") != strings.ReplaceAll(args.DeploymentID, "-", "") || deployment.AppID != app.ID || normalizedScope(deployment.Scope) != normalizedScope(args.Environment) {
		return empty, "", invalid("invalid_response", "deployment does not identify the selected app and environment")
	}
	if deployment.Status != "live" {
		return empty, "", invalid("deployment_not_live", "the selected deployment is not live; inspect that deployment before checking its response")
	}
	request := api.DeploymentResponseCheckRequest{AppID: app.ID, Scope: normalizedScope(deployment.Scope), Path: args.Path, ProjectID: environment.ProjectID, EnvironmentID: environment.ID}
	if err := request.Validate(); err != nil {
		return empty, "", invalid("invalid_response", "app or environment did not identify a valid response-check target")
	}
	return request, deployment.ID, nil
}

func (m *management) requireApplicationResponseSupport(ctx context.Context) error {
	capabilityCtx, cancel := context.WithTimeout(ctx, api.DeploymentResponseCheckPreflightTimeout)
	defer cancel()
	capabilities, err := m.client.GetDeploymentResponseCheckCapabilities(capabilityCtx)
	if err != nil {
		return err
	}
	if capabilities.SupportsCurrentProtocol() {
		return nil
	}
	if capabilities.ProtocolVersion != api.DeploymentResponseCheckProtocolVersion || capabilities.UnavailableReason == api.DeploymentResponseCheckGatewayUnsupported || capabilities.UnavailableReason == api.DeploymentResponseCheckGatewayUnconfigured {
		return invalid("verification_unsupported", "the installation has incompatible application response-check support; update or configure it before checking again")
	}
	return invalid("verification_unavailable", "application response checks are temporarily unavailable; inspect the deployment before checking again")
}

func (m *management) recheckApplicationResponseTarget(ctx context.Context, args applicationResponseArgs, request api.DeploymentResponseCheckRequest) error {
	if err := m.authorizeToolArgs(ctx, args, true); err != nil {
		return err
	}
	current, environment, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, true)
	if err != nil {
		return err
	}
	if current.ID != request.AppID || current.Slug != args.App || environment.ID != request.EnvironmentID || environment.ProjectID != request.ProjectID {
		return invalid("app_access_denied", "app or environment identity changed during the response check; review access again")
	}
	return nil
}

func applicationResponseSchema() map[string]any {
	props := map[string]any{}
	for _, name := range []string{"app_id", "deployment_id", "scope", "project_id", "environment_id", "instance_id", "path", "body"} {
		props[name] = map[string]any{"type": "string"}
	}
	props["schema_version"] = map[string]any{"type": "integer", "const": 1}
	props["http_status"] = map[string]any{"type": "integer", "minimum": 200, "maximum": 599}
	props["body"] = map[string]any{"type": "string", "maxLength": api.DeploymentResponseCheckMaxBodyBytes}
	props["body_truncated"] = map[string]any{"type": "boolean"}
	props["verified"] = map[string]any{"type": "boolean"}
	props["public_access_verified"] = map[string]any{"type": "boolean", "const": false}
	props["checked_at"] = map[string]any{"type": "string", "format": "date-time"}
	return map[string]any{"type": "object", "required": []string{"schema_version", "app_id", "deployment_id", "scope", "instance_id", "http_status", "body", "body_truncated", "path", "checked_at", "verified", "public_access_verified"}, "properties": props, "additionalProperties": false}
}
