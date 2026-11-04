package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type preflightCheck struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type preflightArgs struct {
	App         string `json:"app"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Operation   string `json:"operation,omitempty" jsonschema:"Operation to preview: deploy (default), restart, scale or env_change"`
}

func (m *management) preflightOperation(ctx context.Context, args preflightArgs) (any, error) {
	operation := args.Operation
	if operation == "" {
		operation = "deploy"
	}
	if !api.ValidAppOperation(operation) {
		return nil, invalid("invalid_argument", "operation must be deploy, restart, scale or env_change")
	}
	return m.preflightForOperation(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment}, operation)
}

// preflight deliberately probes only reads. Successful reads cannot establish
// mutation authorization, native qualification, source access or approval.
func (m *management) preflight(ctx context.Context, args appArgs) (any, error) {
	return m.preflightForOperation(ctx, args, "deploy")
}

func (m *management) preflightForOperation(ctx context.Context, args appArgs, operation string) (any, error) {
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if app.ID == "" || app.Slug != args.App || !m.appIDAllowed(app.ID) {
		return nil, invalid("app_access_denied", "app identity is outside consent")
	}
	checks := map[string]preflightCheck{
		"read_consent":            {"passed", "selected_identity_verified"},
		"api_write_authorization": {"unknown", "requires_native_admission"},
		"source_access":           {"unknown", "requires_explicit_source_review"},
		"native_qualification":    {"unknown", "not_established_by_management_reads"},
		"browser_approval":        {"unknown", "requires_review_of_exact_promotion"},
		"write_consent":           {"blocked", "connection_read_only"},
		"durable_state":           {"unknown", "ephemeral_connection"},
	}
	checks["request_read_consent"] = preflightCheck{"blocked", "app_wide_request_diagnostics_not_consented"}
	if m.requestReadAllowed(args.App, app.ID) {
		checks["request_read_consent"] = preflightCheck{"passed", "app_wide_request_consent_only"}
	}
	checks["request_api"] = preflightCheck{"unknown", "requires_exact_request_read_and_native_entitlement"}
	if m.opts.AllowWrites {
		checks["write_consent"] = preflightCheck{"passed", "local_write_consent"}
		allowed := m.opts.Environments == nil
		if m.opts.Environments != nil {
			allowed = m.opts.Environments.DefaultWrite
			if args.Environment != "" {
				allowed = m.opts.Environments.Named[args.Project+"/"+args.Environment].Write
			}
		}
		if !allowed {
			checks["write_consent"] = preflightCheck{"blocked", "environment_write_not_consented"}
		}
	}
	if m.opts.planStore != nil {
		checks["durable_state"] = preflightCheck{"passed", "open_encrypted_single_writer_store"}
		if !m.opts.planStore.available() {
			checks["durable_state"] = preflightCheck{"blocked", "store_unavailable"}
		}
	}
	var review api.AppReleaseReview
	if args.Environment == "" {
		review, err = m.client.GetAppReleaseReview(ctx, args.App)
	} else {
		review, err = m.client.GetAppReleaseReviewInEnvironment(ctx, args.App, args.Environment)
	}
	if err == nil && (review.AppID != app.ID || normalizedScope(review.Scope) != normalizedScope(args.Environment) || review.ProjectID != env.ProjectID || review.EnvironmentID != env.ID || len(review.ConfigurationHash) != 64) {
		err = invalid("invalid_response", "release review identity or configuration is invalid")
	}
	checks["release_review"] = readPreflightCheck(err)
	checks["direct_release"] = preflightCheck{"unknown", "requires_native_admission"}
	if err == nil && review.Protected {
		checks["direct_release"] = preflightCheck{"blocked", "protected_environment_requires_promotion"}
	}
	_, err = m.contextResourceValue(ctx, args, "health")
	checks["scoped_health"] = readPreflightCheck(err)
	_, err = m.getOperationalSummary(ctx, args)
	checks["scoped_operations"] = readPreflightCheck(err)
	_, err = m.getRouteBudgets(ctx, args)
	checks["environment_route_budgets"] = readPreflightCheck(err)
	native, nativeErr := m.client.GetAppOperationPreview(ctx, args.App, operation, args.Environment)
	if nativeErr == nil && !validOperationPreview(native, app.ID, env.ProjectID, env.ID, normalizedScope(args.Environment), operation) {
		nativeErr = invalid("invalid_response", "native authorization preview has incompatible identity or status")
	}
	var preview *api.AppOperationPreview
	if nativeErr == nil {
		preview = &native
		status := "unknown"
		switch native.Authorization {
		case "allowed":
			status = "passed"
		case "blocked":
			status = "blocked"
		}
		checks["api_write_authorization"] = preflightCheck{status, "native_operation_authorization"}
		checks["native_admission"] = preflightCheck{"unknown", "exact_request_and_apply_recheck_required"}
		if native.Admission == "blocked" {
			checks["native_admission"] = preflightCheck{"blocked", "native_operation_blocked"}
		}
	} else {
		checks["native_admission"] = readPreflightCheck(nativeErr)
		checks["api_write_authorization"] = readPreflightCheck(nativeErr)
	}
	return map[string]any{
		"app_id": app.ID, "scope": normalizedScope(args.Environment), "project_id": env.ProjectID, "environment_id": env.ID,
		"checked_at": time.Now().UTC(), "basis": "sequential_read_probes", "checks": checks,
		"operation": operation, "authorization_preview": preview,
		"limitations": "Native authorization is an operation-specific snapshot, not an admission reservation. Other passed reads establish response compatibility. No plans, source reviews, approvals, probes, or writes were performed. Apply rechecks authorization, quotas, protection and current state. Durable status does not verify future disk writes or restart recovery.",
	}, nil
}

func validOperationPreview(p api.AppOperationPreview, appID, projectID, envID, scope, operation string) bool {
	if p.SchemaVersion != 1 || p.AppID != appID || p.ProjectID != projectID || p.EnvironmentID != envID || p.Scope != scope || p.Operation != operation || p.CheckedAt.IsZero() || p.CheckedAt.After(time.Now().Add(time.Minute)) || time.Since(p.CheckedAt) > time.Minute {
		return false
	}
	valid := func(status string) bool { return status == "allowed" || status == "blocked" || status == "unknown" }
	if !valid(p.Authorization) || !valid(p.Admission) || len(p.Checks) == 0 {
		return false
	}
	for _, check := range p.Checks {
		if !valid(check.Status) || check.Reason == "" {
			return false
		}
	}
	requiredScope := api.ScopeDeployWrite
	if operation == "env_change" {
		requiredScope = api.ScopeEnvWrite
	}
	if len(p.RequiredScopes) != 1 || p.RequiredScopes[0] != requiredScope {
		return false
	}
	decision := func(keys []string) string {
		result := "allowed"
		for _, key := range keys {
			check, ok := p.Checks[key]
			if !ok {
				return "unknown"
			}
			if check.Status == "blocked" {
				return "blocked"
			}
			if check.Status != "allowed" {
				result = "unknown"
			}
		}
		return result
	}
	if _, ok := p.Checks["scope"]; !ok {
		return false
	}
	if _, ok := p.Checks["role"]; !ok {
		return false
	}
	if _, ok := p.Checks["email"]; !ok {
		return false
	}
	keys := make([]string, 0, len(p.Checks))
	for key := range p.Checks {
		keys = append(keys, key)
	}
	if p.Authorization != decision([]string{"scope", "role", "email"}) || p.Admission != decision(keys) {
		return false
	}
	return true
}

func readPreflightCheck(err error) preflightCheck {
	if err != nil {
		// Never echo upstream bodies, URLs, credentials or arbitrary diagnostics.
		var upstream *api.APIError
		if errors.As(err, &upstream) {
			switch upstream.Problem.Status {
			case 401, 403:
				return preflightCheck{"blocked", "read_authorization_denied"}
			case 404, 405, 501:
				return preflightCheck{"unknown", "endpoint_or_selected_resource_unavailable"}
			}
		}
		return preflightCheck{"unknown", "read_unavailable_or_incompatible"}
	}
	return preflightCheck{"passed", "validated_read_response"}
}
