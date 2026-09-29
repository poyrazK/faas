package main

// A read-only edge-rule simulation. The evaluator is shared with the
// dashboard so both surfaces show the same selectors and outcomes.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

type edgeRuleTraceResult = edgeruletrace.Result
type edgeRuleTraceRow = edgeruletrace.RuleRow
type edgeRuleTraceSimulation = edgeruletrace.Simulation

func cmdEdgeRulesTrace(args []string) int {
	fs := newFlagSet("edge-rules trace", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	project := fs.String("project", "", "project slug (required with --environment)")
	environment := fs.String("environment", "", "simulate the workload's effective policy in this project environment")
	rawURL := fs.String("url", "", "absolute HTTP(S) request URL")
	scenarioFile := fs.String("config", "", "load a versioned trace scenario JSON file (or - for stdin)")
	method := fs.String("method", http.MethodGet, "request method (default GET)")
	clientIP := fs.String("client-ip", "", "simulated client IP for kind=ip rules")
	country := fs.String("country", "", "simulated ISO 3166-1 alpha-2 country for kind=geo rules")
	bodyFile := fs.String("body-file", "", fmt.Sprintf("read request body from file (max %d bytes; contents are not output)", edgeruletrace.MaxTraceBodyBytes))
	var headerArgs multiFlag
	fs.Var(&headerArgs, "header", "simulated request header (Name:Value; repeat; values compare exactly)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	var input edgeruletrace.Input
	var err error
	if *scenarioFile != "" {
		var conflictingFlags []string
		fs.Visit(func(parsed *flag.Flag) {
			if parsed.Name != "config" {
				conflictingFlags = append(conflictingFlags, "--"+parsed.Name)
			}
		})
		if len(conflictingFlags) > 0 {
			return printErr("Invalid --config", fmt.Errorf("cannot combine --config with request flags %s", strings.Join(conflictingFlags, ", ")))
		}
		configBytes, readErr := readEdgeRuleTraceConfig(*scenarioFile)
		if readErr != nil {
			return printErr("Invalid --config", readErr)
		}
		input, err = edgeruletrace.ParseScenarioConfig(configBytes)
		if err != nil {
			return printErr("Invalid --config", err)
		}
	} else {
		if *slug == "" || *rawURL == "" {
			PrintUsage(os.Stderr, "usage: gregale edge-rules trace (--config <file|-> | --app <slug> --url <http(s)://host/path> [--project <slug> --environment <slug>] [--method GET] [--header Name:Value]... [--client-ip IP] [--country CC] [--body-file <path|->])", "edge-rules")
			return 1
		}
		u, parseErr := url.Parse(*rawURL)
		if parseErr != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
			return printErr("Invalid --url", fmt.Errorf("expected an absolute HTTP(S) URL without credentials or fragment"))
		}
		requestPath := u.Path
		if requestPath == "" {
			requestPath = "/"
		}
		requestHeaders, parseErr := edgeruletrace.ParseRequestHeaders(headerArgs)
		if parseErr != nil {
			return printErr("Invalid --header", parseErr)
		}
		var requestBody []byte
		bodyProvided := *bodyFile != ""
		if bodyProvided {
			requestBody, err = readEdgeRuleTraceBody(*bodyFile)
			if err != nil {
				return printErr("Invalid --body-file", err)
			}
		}
		input, err = edgeruletrace.NormalizeInput(edgeruletrace.Input{
			Project: *project, Environment: *environment,
			App: *slug, Host: u.Hostname(), Path: requestPath, Method: *method,
			ClientIP: *clientIP, Country: *country, Headers: requestHeaders,
			Body: requestBody, BodyProvided: bodyProvided,
		})
		if err != nil {
			return printErr("Invalid trace input", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, appErr := client.GetApp(context.Background(), input.App)
	if appErr != nil {
		return printErr("App lookup failed", appErr)
	}
	input.AppMaintenanceLoaded = true
	input.AppMaintenanceMode = app.MaintenanceMode
	input.AsyncWorkloadContextLoaded = true
	input.AsyncRequestInvocationsEnabled = app.WorkloadClass != "worker" && app.WorkloadClass != "job" && app.Manifest.ExecutionMode != api.ExecutionModeWorker && app.Manifest.ExecutionMode != api.ExecutionModeJob
	input.AsyncAppRetryPolicyLoaded = true
	input.AsyncAppRetryPolicy = app.RetryPolicy
	input.AppRequestBudgetLoaded = app.EffectiveLimits.RequestBudgetMS > 0 && app.EffectiveLimits.RequestBudgetMaxMS > 0
	input.RequestBudgetMS = app.EffectiveLimits.RequestBudgetMS
	input.RequestBudgetMaxMS = app.EffectiveLimits.RequestBudgetMaxMS
	input.RequestTimeoutS = app.RequestTimeoutS
	input.AppThrottleContextLoaded = app.EffectiveLimits.AppRequestRateRPS > 0 && app.EffectiveLimits.AppRequestBurst > 0
	input.AppRequestRateRPS = app.EffectiveLimits.AppRequestRateRPS
	input.AppRequestRateBurst = app.EffectiveLimits.AppRequestBurst
	input.AccountRequestRateRPM = app.EffectiveLimits.AccountRequestRateRPM
	input.OnlyAllowDeclaredRoutes = app.OnlyAllowDeclaredRoutes
	input.DeclaredRoutes = append([]api.DeclaredRoute(nil), app.DeclaredRoutes...)
	var environmentWorkload *api.ProjectEnvironmentStateWorkloadResponse
	if input.Environment != "" {
		environmentState, stateErr := client.GetProjectEnvironmentState(context.Background(), input.Project, input.Environment)
		if stateErr != nil {
			return printErr("Environment lookup failed", stateErr)
		}
		if environmentState.ProjectSlug != input.Project || environmentState.Environment != input.Environment {
			return printErr("Environment lookup failed", fmt.Errorf("server returned project %q environment %q for the requested selection", environmentState.ProjectSlug, environmentState.Environment))
		}
		for i := range environmentState.Workloads {
			if environmentState.Workloads[i].WorkloadSlug == input.App {
				environmentWorkload = &environmentState.Workloads[i]
				break
			}
		}
		if environmentWorkload == nil {
			return printErr("Environment lookup failed", fmt.Errorf("app %q is not a workload in project %q", input.App, input.Project))
		}
		input, err = edgeruletrace.ApplyEnvironmentRoutePolicy(input, environmentWorkload.Routes)
		if err != nil {
			return printErr("Environment route policy unavailable", err)
		}
	}
	if input.OnlyAllowDeclaredRoutes && len(input.DeclaredRoutes) == 0 && !input.DeclaredRoutesLoaded {
		doc, docErr := client.GetAppOpenAPI(context.Background(), input.App, "manual_import")
		if docErr == nil {
			input.DeclaredRouteDocumentLoaded = true
			input.DeclaredRouteOpenAPIDoc = append([]byte(nil), doc...)
		} else {
			var apiErr *api.APIError
			if errors.As(docErr, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
				input.DeclaredRouteDocumentMissing = true
			}
		}
	}
	input.AppCORSDefaultsLoaded = true
	input.CORSDefaultEnabled = app.CORSDefaultEnabled
	input.CORSDefaultOrigins = append([]string(nil), app.CORSDefaultOrigins...)
	if input.BodyProvided {
		input.RequestBodyMaxBytes = app.EffectiveLimits.RequestBodyMaxBytes
	}
	input, err = edgeruletrace.NormalizeInput(input)
	if err != nil {
		return printErr("Invalid trace input", err)
	}
	rules, err := client.ListEdgeRulesForApp(context.Background(), input.App)
	if err != nil {
		return printErr("List failed", err)
	}
	if edgeRuleTraceHasEnabledKind(rules, "throttle") || edgeRuleTraceHasEnabledKind(rules, "async") {
		// The app response carries effective app/account ceilings, but the
		// account profile supplies plan-only throttle and async limits. If it
		// is temporarily unavailable, keep the trace useful and mark those
		// ceilings unavailable instead of guessing.
		if account, accountErr := client.Whoami(context.Background()); accountErr == nil {
			if limits, ok := api.LimitsFor(api.Plan(account.Plan)); ok {
				if edgeRuleTraceHasEnabledKind(rules, "throttle") {
					input.ThrottlePlanLimitsLoaded = limits.RateLimitRPS > 0 && limits.RateLimitBurst > 0
					input.ThrottlePlanMaxRPS = limits.RateLimitRPS
					input.ThrottlePlanMaxBurst = limits.RateLimitBurst
				}
				if edgeRuleTraceHasEnabledKind(rules, "async") {
					input.AsyncPlanLimitsLoaded = true
					input.AsyncPlan = api.Plan(account.Plan)
					input.AsyncInvokeAllowed = limits.AsyncInvokeAllowed
					input.AsyncMaxPayloadBytes = limits.MaxSourceBytesPerInvocation
					input.AsyncMaxQueueAttempts = limits.MaxQueueAttempts
					input.AsyncMaxDeadlineSeconds = limits.MaxAsyncInvocationDeadlineSeconds
				}
			}
		}
	}
	if environmentWorkload != nil {
		rules, err = edgeruletrace.ApplyEnvironmentEdgePolicy(input, rules, environmentWorkload.Policies, environmentWorkload.Release.URL, environmentWorkload.Domains)
		if err != nil {
			return printErr("Environment edge policy unavailable", err)
		}
	}
	if edgeruletrace.RequiresCorsPresetData(rules) {
		presets, presetErr := client.ListCorsPresets(context.Background(), "")
		if presetErr != nil {
			return printErr("CORS preset lookup failed", presetErr)
		}
		input.CorsPresets = presets.Presets
	}
	result, err := edgeruletrace.Simulate(input, rules)
	if err != nil {
		return printErr("Invalid trace input", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderEdgeRuleTrace(result)
	return 0
}

func edgeRuleTraceHasEnabledKind(rules []api.EdgeRuleResponse, kind string) bool {
	for _, rule := range rules {
		if rule.Enabled && rule.Kind == kind {
			return true
		}
	}
	return false
}

func renderEdgeRuleTrace(result edgeruletrace.Result) {
	if result.Environment != "" {
		_, _ = fmt.Fprintf(osStdout, "%s %s%s (app %s, project %s, environment %s)\n", result.Method, result.Host, result.Path, result.App, result.Project, result.Environment)
	} else {
		_, _ = fmt.Fprintf(osStdout, "%s %s%s (app %s)\n", result.Method, result.Host, result.Path, result.App)
	}
	if result.BodyProvided {
		_, _ = fmt.Fprintf(osStdout, "request body: supplied (%d bytes; contents withheld)\n", result.BodyBytes)
	}
	if result.ClientIP != "" || result.Country != "" {
		_, _ = fmt.Fprintf(osStdout, "simulated context: client_ip=%s country=%s\n", emptyAsDash(result.ClientIP), emptyAsDash(result.Country))
	}
	headerNames := sortedHeaderNames(result.Headers)
	for _, name := range headerNames {
		for _, value := range result.Headers[name] {
			_, _ = fmt.Fprintf(osStdout, "header: %s=%q\n", name, value)
		}
	}
	if len(result.Rules) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no edge rules for this app)")
	} else {
		for _, row := range result.Rules {
			_, _ = fmt.Fprintf(osStdout, "%-18s %-16s outcome=%-16s priority=%-5d %s — %s; %s\n", row.Kind, row.Status, row.Outcome, row.Priority, row.ID, row.Reason, row.OutcomeReason)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "simulation: status=%s outcome=%s final_path=%s\n", result.Simulation.Status, result.Simulation.Outcome, result.Simulation.FinalPath)
	for _, step := range result.Simulation.Steps {
		label := step.RuleID
		if label == "" {
			label = step.Kind
		}
		_, _ = fmt.Fprintf(osStdout, "  %-12s %-12s %s — %s\n", step.Phase, step.Outcome, label, step.Reason)
		if step.BudgetPolicy != nil {
			policy := step.BudgetPolicy
			_, _ = fmt.Fprintf(osStdout, "    request budget: %d ms effective (configured %d ms; plan ceiling %d ms; source %s; override %s)\n",
				policy.BudgetMS, policy.ConfiguredMS, policy.PlanMaxMS, policy.Source, policy.OverrideStatus)
			if policy.TotalDeadlineMS > 0 {
				_, _ = fmt.Fprintf(osStdout, "    total deadline: %d ms from public ingress (includes upload/wake/queue; execution overrides cannot increase it)\n", policy.TotalDeadlineMS)
			}
		}
		if step.ThrottlePolicy != nil {
			policy := step.ThrottlePolicy
			_, _ = fmt.Fprintf(osStdout, "    route throttle: configured %.3g requests/s, burst %d (gateway effective %.3g requests/s, burst %d); key_by=%s; missing_key_policy=%s; max_keys_per_rule=%d (%s)\n",
				policy.RequestsPerSecond, policy.Burst, policy.GatewayRateRPS, policy.GatewayBurst, policy.KeyBy, policy.MissingKeyPolicy, policy.MaxKeysPerRule, policy.MaxKeysSource)
			if policy.PlanMaxRPS > 0 && policy.PlanMaxBurst > 0 {
				_, _ = fmt.Fprintf(osStdout, "    route-throttle plan ceiling: %d requests/s, burst %d (%s)\n", policy.PlanMaxRPS, policy.PlanMaxBurst, policy.PlanCeilingStatus)
			}
			if policy.AppRequestRPS > 0 && policy.AppRequestBurst > 0 {
				_, _ = fmt.Fprintf(osStdout, "    separate app-wide cap: %d requests/s, burst %d; account-wide cap: %d requests/min\n",
					policy.AppRequestRPS, policy.AppRequestBurst, policy.AccountRequestRPM)
			}
		}
		if step.RetryPolicy != nil {
			policy := step.RetryPolicy
			_, _ = fmt.Fprintf(osStdout, "    retry policy: up to %d total attempts (%d replay(s), %s); min remaining budget %d ms; backoff %d ms; aggregate budget %d%% or at least %d replay(s); allow_non_idempotent=%t; method eligibility=%s; idempotency_key_present=%t\n",
				policy.MaxAttempts, policy.MaxReplays, policy.MaxAttemptsSource, policy.MinRemainingMS, policy.BackoffMS,
				policy.BudgetPercent, policy.BudgetMinRetries, policy.AllowNonIdempotent, policy.MethodEligibility, policy.IdempotencyKeyPresent)
		}
		if step.AsyncPolicy != nil {
			policy := step.AsyncPolicy
			deadline := "unresolved"
			if policy.MaxAgeSource == "no_deadline" {
				deadline = "none"
			} else if policy.EffectiveMaxAgeSeconds > 0 {
				deadline = fmt.Sprintf("%d s (%s)", policy.EffectiveMaxAgeSeconds, policy.MaxAgeSource)
			}
			_, _ = fmt.Fprintf(osStdout, "    async route: request gate=%s; plan=%s; workload=%s; payload=%s; deadline=%s; retry=%s (%s attempts, %s); idempotency_key_present=%t; callbacks success=%t failure=%t\n",
				policy.RequestGate, policy.PlanGate, policy.WorkloadGate, policy.PayloadStatus, deadline,
				policy.RetryPolicySource, asyncTraceAttempts(policy), policy.MaxAttemptsStatus,
				policy.IdempotencyKeyPresent, policy.OnSuccessConfigured, policy.OnFailureConfigured)
			if policy.MaxPayloadBytes > 0 {
				_, _ = fmt.Fprintf(osStdout, "      plan payload limit: %d bytes\n", policy.MaxPayloadBytes)
			}
		}
		if step.JWTPolicy != nil {
			policy := step.JWTPolicy
			_, _ = fmt.Fprintf(osStdout, "    JWT policy: bearer_token_present=%t; issuer_configured=%t; jwks_configured=%t; audiences=%d; algorithms=[%s]; required_claim_names=[%s]; platform_tenant_external_ref_claim_configured=%t\n",
				policy.BearerTokenPresent, policy.IssuerConfigured, policy.JWKSConfigured, policy.AudienceCount,
				strings.Join(policy.Algorithms, ","), strings.Join(policy.RequiredClaimNames, ","), policy.PlatformTenantExternalRefClaimConfigured)
		}
	}
	if result.Simulation.StatusCode != 0 {
		_, _ = fmt.Fprintf(osStdout, "  response: status=%d", result.Simulation.StatusCode)
		if result.Simulation.ProblemCode != "" {
			_, _ = fmt.Fprintf(osStdout, " problem_code=%s", result.Simulation.ProblemCode)
		}
		if result.Simulation.Location != "" {
			_, _ = fmt.Fprintf(osStdout, " location=%q", result.Simulation.Location)
		}
		if result.Simulation.RetryAfterSeconds != 0 {
			_, _ = fmt.Fprintf(osStdout, " retry_after=%d", result.Simulation.RetryAfterSeconds)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	if result.Simulation.Message != "" {
		_, _ = fmt.Fprintf(osStdout, "  response message: %q\n", result.Simulation.Message)
	}
	for _, name := range sortedStringMapKeys(result.Simulation.RedirectHeaders) {
		_, _ = fmt.Fprintf(osStdout, "  redirect header: %s=%q\n", name, result.Simulation.RedirectHeaders[name])
	}
	if result.Simulation.TargetApp != "" {
		_, _ = fmt.Fprintf(osStdout, "  target app: %s\n", result.Simulation.TargetApp)
	}
	for _, op := range result.Simulation.ResponseHeaderOps {
		_, _ = fmt.Fprintf(osStdout, "  response header op: %s %s=%q\n", op.Action, op.Name, op.Value)
	}
	for _, name := range sortedHeaderNames(result.Simulation.RequestHeaders) {
		for _, value := range result.Simulation.RequestHeaders[name] {
			_, _ = fmt.Fprintf(osStdout, "  simulated request header: %s=%q\n", name, value)
		}
	}
	if result.Simulation.StoppedAt != "" {
		_, _ = fmt.Fprintf(osStdout, "  simulation stopped at %s: %s\n", result.Simulation.StoppedAt, result.Simulation.Reason)
	}
	_, _ = fmt.Fprintln(osStdout, result.Scope)
}

func asyncTraceAttempts(policy *edgeruletrace.AsyncPolicyPreview) string {
	if policy.MaxAttemptsStatus != "effective" {
		return "unresolved"
	}
	return fmt.Sprintf("%d total (%d replay(s))", policy.EffectiveMaxAttempts, policy.MaxReplays)
}

func readEdgeRuleTraceBody(path string) ([]byte, error) {
	reader := osStdin
	var file *os.File
	if path != "-" {
		opened, err := openCustomerFile(path)
		if err != nil {
			return nil, err
		}
		file = opened
		defer func() { _ = file.Close() }()
		reader = file
	}
	body, err := io.ReadAll(io.LimitReader(reader, int64(edgeruletrace.MaxTraceBodyBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("could not read request body")
	}
	if len(body) > edgeruletrace.MaxTraceBodyBytes {
		return nil, fmt.Errorf("request body exceeds the %d-byte trace limit", edgeruletrace.MaxTraceBodyBytes)
	}
	return body, nil
}

func readEdgeRuleTraceConfig(path string) ([]byte, error) {
	reader := osStdin
	var file *os.File
	if path != "-" {
		opened, err := openCustomerFile(path)
		if err != nil {
			return nil, err
		}
		file = opened
		defer func() { _ = file.Close() }()
		reader = file
	}
	config, err := io.ReadAll(io.LimitReader(reader, int64(edgeruletrace.MaxScenarioConfigBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("could not read trace scenario config")
	}
	if len(config) > edgeruletrace.MaxScenarioConfigBytes {
		return nil, fmt.Errorf("trace scenario config exceeds the %d-byte limit", edgeruletrace.MaxScenarioConfigBytes)
	}
	return config, nil
}

func sortedHeaderNames(headers map[string][]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedStringMapKeys(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// These aliases preserve compact CLI tests while the implementation itself
// lives in the shared package used by the dashboard.
func previewEdgeRules(app, host, requestPath, method, clientIP, country string, rules []api.EdgeRuleResponse, suppliedHeaders ...http.Header) edgeRuleTraceResult {
	var headers http.Header
	if len(suppliedHeaders) > 0 {
		headers = suppliedHeaders[0]
	}
	result, _ := edgeruletrace.Simulate(edgeruletrace.Input{
		App: app, Host: host, Path: requestPath, Method: method, ClientIP: clientIP, Country: country, Headers: headers, AppMaintenanceLoaded: true,
	}, rules)
	return result
}

func simulateEdgeRuleRequest(host, requestPath, method, clientIP, country string, rules []api.EdgeRuleResponse, headers http.Header) edgeRuleTraceSimulation {
	return previewEdgeRules("test", host, requestPath, method, clientIP, country, rules, headers).Simulation
}

func traceHostMatches(pattern, host string) bool { return edgeruletrace.HostMatches(pattern, host) }
func traceMethodMatches(methods []string, method string) bool {
	return edgeruletrace.MethodMatches(methods, method)
}
func emptyAsDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
