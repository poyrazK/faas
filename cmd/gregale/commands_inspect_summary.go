package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

const inspectSummarySchemaVersion = 1

type inspectSummary struct {
	SchemaVersion   int                     `json:"schema_version"`
	App             inspectAppSummary       `json:"app"`
	Runtime         inspectRuntimeSummary   `json:"runtime"`
	Resources       inspectResourceSummary  `json:"resources"`
	API             inspectAPISummary       `json:"api"`
	Data            inspectDataSummary      `json:"data"`
	Release         inspectReleaseSummary   `json:"release"`
	Recommendations []inspectRecommendation `json:"recommendations"`
	Unavailable     []string                `json:"unavailable,omitempty"`
}

type inspectAppSummary struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	URL           string `json:"url"`
	Status        string `json:"status"`
	Type          string `json:"type"`
	Runtime       string `json:"runtime,omitempty"`
	WorkloadClass string `json:"workload_class,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

type inspectRuntimeSummary struct {
	Framework        string                  `json:"framework,omitempty"`
	FrameworkVersion string                  `json:"framework_version,omitempty"`
	Entrypoint       string                  `json:"entrypoint,omitempty"`
	Port             int                     `json:"port,omitempty"`
	HealthPath       string                  `json:"health_path,omitempty"`
	Source           string                  `json:"source,omitempty"`
	Warnings         []inspectRuntimeWarning `json:"warnings,omitempty"`
	Health           *inspectHealthSummary   `json:"health,omitempty"`
}

type inspectRuntimeWarning struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Sources []string `json:"sources,omitempty"`
}

type inspectHealthSummary struct {
	Status     string `json:"status"`
	Path       string `json:"path,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	VerifiedAt string `json:"verified_at,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

type inspectResourceSummary struct {
	Profile           string             `json:"profile"`
	MemoryMB          int                `json:"memory_mb"`
	CPUMillicores     int                `json:"cpu_millicores"`
	ScaleToZero       bool               `json:"scale_to_zero"`
	MinInstances      int                `json:"min_instances"`
	MaxInstances      int                `json:"max_instances"`
	Target            *api.ScalingTarget `json:"target,omitempty"`
	ScaleOutCooldownS int                `json:"scale_out_cooldown_s,omitempty"`
	ScaleInCooldownS  int                `json:"scale_in_cooldown_s,omitempty"`
}

type inspectAPISummary struct {
	Available      bool   `json:"available"`
	Source         string `json:"source,omitempty"`
	OpenAPIVersion string `json:"openapi_version,omitempty"`
	Paths          int    `json:"paths"`
	Endpoints      int    `json:"endpoints"`
}

type inspectDataSummary struct {
	Available     bool     `json:"available"`
	Upstreams     int      `json:"upstreams"`
	UpstreamQuota int      `json:"upstream_quota"`
	Kinds         []string `json:"kinds"`
}

type inspectReleaseSummary struct {
	Available              bool   `json:"available"`
	DeploymentID           string `json:"deployment_id,omitempty"`
	Status                 string `json:"status,omitempty"`
	Scope                  string `json:"scope,omitempty"`
	TrafficPercent         int    `json:"traffic_percent,omitempty"`
	CanaryPreset           string `json:"canary_preset,omitempty"`
	CanaryStep             int    `json:"canary_step,omitempty"`
	CanaryTotalSteps       int    `json:"canary_total_steps,omitempty"`
	RolloutState           string `json:"rollout_state,omitempty"`
	RollbackOn5xx          bool   `json:"rollback_on_5xx"`
	HealthSignalsAvailable bool   `json:"health_signals_available"`
	HealthGateRules        int    `json:"health_gate_rules"`
	FiringHealthGates      int    `json:"firing_health_gates"`
}

type inspectRecommendation struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Next     string `json:"next,omitempty"`
}

type inspectSummaryInputs struct {
	Deployment    *api.DeploymentResponse
	DeploymentErr error
	OpenAPIRaw    []byte
	OpenAPIErr    error
	Upstreams     []api.DataUpstreamResponse
	UpstreamCount int
	UpstreamQuota int
	UpstreamsErr  error
	Alerts        []api.AlertRuleResponse
	AlertsErr     error
}

func cmdInspectSummary(slug string) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not load app", err)
	}
	inputs := loadInspectSummaryInputs(ctx, client, app)
	summary := buildInspectSummary(app, inputs)
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	renderInspectSummaryHuman(osStdout, summary)
	return 0
}

func loadInspectSummaryInputs(ctx context.Context, client *Client, app api.AppResponse) inspectSummaryInputs {
	var out inspectSummaryInputs
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		deployment, err := client.GetLatestAppDeployment(ctx, app.Slug)
		if isNotFound(err) {
			return
		}
		if err != nil {
			out.DeploymentErr = err
			return
		}
		out.Deployment = &deployment
	}()
	go func() {
		defer wg.Done()
		out.OpenAPIRaw, out.OpenAPIErr = client.GetAppOpenAPI(ctx, app.Slug, "auto")
	}()
	go func() {
		defer wg.Done()
		out.Upstreams, out.UpstreamCount, out.UpstreamQuota, out.UpstreamsErr = client.ListAppDataUpstreamsWithQuota(ctx, app.Slug, "")
	}()
	go func() {
		defer wg.Done()
		out.Alerts, out.AlertsErr = client.ListAlertRules(ctx, app.Slug)
	}()
	wg.Wait()
	return out
}

func buildInspectSummary(app api.AppResponse, in inspectSummaryInputs) inspectSummary {
	summary := inspectSummary{
		SchemaVersion: inspectSummarySchemaVersion,
		App: inspectAppSummary{
			ID: app.ID, Slug: app.Slug, URL: app.URL, Status: app.Status,
			Type: app.Type, Runtime: app.Runtime, WorkloadClass: app.WorkloadClass, Protocol: app.AppProtocol,
		},
		Resources:   inspectResources(app),
		API:         inspectOpenAPI(in.OpenAPIRaw),
		Data:        inspectData(in),
		Release:     inspectRelease(app.ID, in.Deployment, in.Alerts, in.AlertsErr == nil),
		Unavailable: inspectUnavailable(in),
	}
	summary.Runtime = inspectRuntime(app, in.Deployment, &summary.Unavailable)
	if in.OpenAPIErr != nil {
		summary.API = inspectAPISummary{}
	} else if len(in.OpenAPIRaw) > 0 && !summary.API.Available {
		summary.Unavailable = append(summary.Unavailable, "openapi")
	}
	summary.Unavailable = sortedUniqueStrings(summary.Unavailable)
	summary.Recommendations = inspectRecommendations(summary)
	return summary
}

func inspectResources(app api.AppResponse) inspectResourceSummary {
	profile := string(app.ResourceProfile)
	if profile == "" {
		profile = "custom"
	}
	out := inspectResourceSummary{
		Profile: profile, MemoryMB: app.RAMMB, CPUMillicores: app.CPUMillicores,
		MinInstances: app.MinInstances, MaxInstances: app.MaxConcurrency,
	}
	if app.ScalingPolicy != nil {
		out.MinInstances = app.ScalingPolicy.MinInstances
		if app.ScalingPolicy.MaxInstances > 0 {
			out.MaxInstances = app.ScalingPolicy.MaxInstances
		}
		out.Target = app.ScalingPolicy.Target
		out.ScaleOutCooldownS = app.ScalingPolicy.ScaleOutCooldownS
		out.ScaleInCooldownS = app.ScalingPolicy.ScaleInCooldownS
	}
	out.ScaleToZero = out.MinInstances == 0
	return out
}

func inspectRuntime(app api.AppResponse, dep *api.DeploymentResponse, unavailable *[]string) inspectRuntimeSummary {
	out := inspectRuntimeSummary{
		Entrypoint: strings.Join(app.Manifest.Entrypoint, " "),
		Port:       app.Manifest.Port, HealthPath: app.Manifest.Healthz, Source: "app_manifest",
	}
	if dep == nil {
		return out
	}
	if dep.BuildPlan != nil {
		if dep.BuildPlan.Framework != "" {
			out.Framework = dep.BuildPlan.Framework
		}
		if dep.BuildPlan.Version != "" {
			out.FrameworkVersion = dep.BuildPlan.Version
		}
		if dep.BuildPlan.Entrypoint != "" {
			out.Entrypoint = dep.BuildPlan.Entrypoint
		}
		if dep.BuildPlan.Port > 0 {
			out.Port = dep.BuildPlan.Port
		}
		if dep.BuildPlan.HealthPath != "" {
			out.HealthPath = dep.BuildPlan.HealthPath
		}
		out.Source = "build_plan"
	}
	if len(dep.APIHostingReceipt) == 0 {
		return out
	}
	receipt, err := apihostingreceipt.Decode(dep.APIHostingReceipt)
	if err != nil {
		*unavailable = append(*unavailable, "hosting_receipt")
		return out
	}
	mergeHostingReceipt(&out, receipt)
	return out
}

func mergeHostingReceipt(out *inspectRuntimeSummary, receipt apihostingreceipt.Receipt) {
	profile := receipt.Profile
	if profile.Framework != "" {
		out.Framework = profile.Framework
	}
	if profile.FrameworkVer != "" {
		out.FrameworkVersion = profile.FrameworkVer
	}
	if profile.StartCommand != "" {
		out.Entrypoint = profile.StartCommand
	}
	if profile.Port > 0 {
		out.Port = profile.Port
	}
	if profile.HealthPath != "" {
		out.HealthPath = profile.HealthPath
	}
	for _, warning := range profile.Warnings {
		out.Warnings = append(out.Warnings, inspectRuntimeWarning{Code: warning.Code, Message: warning.Message, Sources: warning.Sources})
	}
	out.Source = "hosting_receipt"
	healthPath := receipt.Smoke.Path
	if healthPath == "" {
		healthPath = out.HealthPath
	}
	out.Health = &inspectHealthSummary{
		Status: receipt.Smoke.Status, Path: healthPath, StatusCode: receipt.Smoke.StatusCode,
		LatencyMS: receipt.Smoke.LatencyMS, VerifiedAt: receipt.Smoke.VerifiedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		RequestID: receipt.Smoke.RequestID, ErrorCode: receipt.Smoke.ErrorCode,
	}
	if receipt.Smoke.VerifiedAt.IsZero() {
		out.Health.VerifiedAt = ""
	}
}

func inspectOpenAPI(raw []byte) inspectAPISummary {
	if len(raw) == 0 {
		return inspectAPISummary{}
	}
	var doc struct {
		OpenAPI string                                `json:"openapi"`
		Swagger string                                `json:"swagger"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return inspectAPISummary{}
	}
	version := doc.OpenAPI
	if version == "" {
		version = doc.Swagger
	}
	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}
	endpoints := 0
	for _, path := range doc.Paths {
		for method := range path {
			if methods[strings.ToLower(method)] {
				endpoints++
			}
		}
	}
	return inspectAPISummary{Available: true, Source: "auto", OpenAPIVersion: version, Paths: len(doc.Paths), Endpoints: endpoints}
}

func inspectData(in inspectSummaryInputs) inspectDataSummary {
	out := inspectDataSummary{Available: in.UpstreamsErr == nil, Upstreams: in.UpstreamCount, UpstreamQuota: in.UpstreamQuota, Kinds: []string{}}
	seen := map[string]bool{}
	for _, upstream := range in.Upstreams {
		kind := string(upstream.Kind)
		if kind != "" && !seen[kind] {
			seen[kind] = true
			out.Kinds = append(out.Kinds, kind)
		}
	}
	sort.Strings(out.Kinds)
	return out
}

func inspectRelease(appID string, dep *api.DeploymentResponse, alerts []api.AlertRuleResponse, alertsAvailable bool) inspectReleaseSummary {
	out := inspectReleaseSummary{HealthSignalsAvailable: alertsAvailable}
	if dep != nil {
		out.Available = true
		out.DeploymentID, out.Status, out.Scope = dep.ID, dep.Status, dep.Scope
		out.TrafficPercent, out.CanaryPreset = dep.TrafficPercent, dep.CanaryPreset
		out.CanaryStep, out.CanaryTotalSteps = dep.CanaryStep, dep.CanaryTotalSteps
		out.RolloutState, out.RollbackOn5xx = dep.RolloutState, dep.RollbackOn5xx
		if out.CanaryPreset == "" {
			out.CanaryPreset = "none"
		}
	}
	for _, rule := range alerts {
		if rule.AppID != appID || !rule.Enabled || (rule.Action != "rollback" && rule.Action != "demote") {
			continue
		}
		out.HealthGateRules++
		if rule.State == "firing" {
			out.FiringHealthGates++
		}
	}
	return out
}

func inspectUnavailable(in inspectSummaryInputs) []string {
	var out []string
	for _, row := range []struct {
		name string
		err  error
	}{{"deployment", in.DeploymentErr}, {"openapi", in.OpenAPIErr}, {"upstreams", in.UpstreamsErr}, {"alerts", in.AlertsErr}} {
		if row.err != nil {
			out = append(out, row.name)
		}
	}
	return out
}

func inspectRecommendations(summary inspectSummary) []inspectRecommendation {
	out := make([]inspectRecommendation, 0)
	add := func(code, severity, message, next string) {
		out = append(out, inspectRecommendation{Code: code, Severity: severity, Message: message, Next: next})
	}
	if !summary.Release.Available && !containsString(summary.Unavailable, "deployment") {
		add("deploy_required", "warning", "No deployment was found for this app.", "Run `gregale deploy` from the application source directory.")
	} else if summary.Release.Status == "failed" {
		add("deployment_failed", "error", "The latest deployment failed.", "Run `gregale inspect "+summary.App.Slug+" --errors` for the persisted explanation.")
	} else if summary.Release.Status == "live" && (summary.Runtime.Health == nil || summary.Runtime.Health.Status != apihostingreceipt.SmokeVerified) {
		if summary.Runtime.Health != nil && (summary.Runtime.Health.ErrorCode == apihostingreceipt.SmokeErrorNotConfigured || summary.Runtime.Health.ErrorCode == apihostingreceipt.SmokeErrorVerifierNotConfigured) {
			add("health_verifier_unconfigured", "error", "The public smoke verifier is not configured, so this deployment is not externally verified.", "An operator must configure FAAS_API_HOSTING_SMOKE_URL on the compute node and redeploy.")
		} else if summary.Runtime.Health != nil && summary.Runtime.Health.ErrorCode == "smoke_health_path_missing" {
			add("health_endpoint_missing", "warning", "The application has no configured health endpoint.", "Expose a health endpoint or set an explicit health path, then redeploy.")
		} else {
			add("health_unverified", "warning", "The live deployment has no verified health receipt.", "Configure a health endpoint and redeploy.")
		}
	}
	if summary.App.Type == "app" && (summary.Runtime.Framework == "" || summary.Runtime.Framework == "unknown") {
		add("framework_unknown", "warning", "Gregale could not identify the application framework.", "Set an explicit start command or Dockerfile.")
	}
	for _, warning := range summary.Runtime.Warnings {
		add("profile_"+warning.Code, "warning", warning.Message, "Review the inferred deployment profile before the next release.")
	}
	if summary.API.Available && summary.API.Endpoints == 0 && (summary.App.WorkloadClass == "http" || summary.App.WorkloadClass == "graphql") {
		add("openapi_empty", "info", "No API endpoints are represented in the generated OpenAPI document.", "Expose /openapi.json or import an OpenAPI document.")
	}
	canary := summary.Release.CanaryPreset != "" && summary.Release.CanaryPreset != "none"
	if summary.Release.Status == "live" && summary.Release.Scope == "production" && !canary {
		add("safe_release_missing", "warning", "The production deployment has no canary policy.", "Deploy with `--canary-preset balanced`.")
	}
	if canary && summary.Release.HealthSignalsAvailable && summary.Release.HealthGateRules == 0 {
		add("canary_without_health_gate", "warning", "The canary has no rollback or demote health rule.", "Add an actionable latency or error-rate alert.")
	}
	if summary.Release.FiringHealthGates > 0 {
		add("health_gate_firing", "error", "A safe-release health gate is currently firing.", "Inspect the alert and deployment audit before promoting.")
	}
	return out
}

func sortedUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func renderInspectSummaryHuman(w io.Writer, summary inspectSummary) {
	_, _ = fmt.Fprintf(w, "%s\n", summary.App.Slug)
	_, _ = fmt.Fprintf(w, "  app:       %s · %s · %s\n", fallback(summary.App.Status), fallback(summary.App.Type), fallback(summary.App.WorkloadClass))
	_, _ = fmt.Fprintf(w, "  url:       %s\n", fallback(summary.App.URL))
	renderInspectRuntime(w, summary.Runtime)
	renderInspectResources(w, summary.Resources)
	renderInspectSignals(w, summary)
	renderInspectRelease(w, summary.Release, containsString(summary.Unavailable, "deployment"))
	if len(summary.Unavailable) > 0 {
		_, _ = fmt.Fprintf(w, "  unavailable: %s\n", strings.Join(summary.Unavailable, ", "))
	}
	renderInspectRecommendations(w, summary.Recommendations)
}

func renderInspectRuntime(w io.Writer, runtime inspectRuntimeSummary) {
	framework := fallback(runtime.Framework)
	if runtime.FrameworkVersion != "" {
		framework += " " + runtime.FrameworkVersion
	}
	_, _ = fmt.Fprintf(w, "  runtime:   %s · port %s · %s\n", framework, intOrDash(runtime.Port), fallback(runtime.Source))
	_, _ = fmt.Fprintf(w, "  start:     %s\n", fallback(runtime.Entrypoint))
	if runtime.Health == nil {
		_, _ = fmt.Fprintf(w, "  health:    %s · not verified\n", fallback(runtime.HealthPath))
		return
	}
	_, _ = fmt.Fprintf(w, "  health:    %s · %s", fallback(runtime.Health.Path), runtime.Health.Status)
	if runtime.Health.StatusCode > 0 {
		_, _ = fmt.Fprintf(w, " · %d", runtime.Health.StatusCode)
	}
	if runtime.Health.LatencyMS > 0 {
		_, _ = fmt.Fprintf(w, " · %dms", runtime.Health.LatencyMS)
	}
	_, _ = fmt.Fprintln(w)
}

func renderInspectResources(w io.Writer, resources inspectResourceSummary) {
	_, _ = fmt.Fprintf(w, "  resources: %s · %d MB · %d mCPU\n", resources.Profile, resources.MemoryMB, resources.CPUMillicores)
	target := "request-driven"
	if resources.Target != nil && resources.Target.Metric != "" {
		target = fmt.Sprintf("%s=%g", resources.Target.Metric, resources.Target.Value)
	}
	_, _ = fmt.Fprintf(w, "  scaling:   %d→%d instances · %s\n", resources.MinInstances, resources.MaxInstances, target)
}

func renderInspectSignals(w io.Writer, summary inspectSummary) {
	if summary.API.Available {
		_, _ = fmt.Fprintf(w, "  api:       OpenAPI %s · %d paths · %d endpoints\n", fallback(summary.API.OpenAPIVersion), summary.API.Paths, summary.API.Endpoints)
	} else {
		_, _ = fmt.Fprintln(w, "  api:       unavailable")
	}
	if summary.Data.Available {
		kinds := "none"
		if len(summary.Data.Kinds) > 0 {
			kinds = strings.Join(summary.Data.Kinds, ", ")
		}
		_, _ = fmt.Fprintf(w, "  data:      %d/%d upstreams · %s\n", summary.Data.Upstreams, summary.Data.UpstreamQuota, kinds)
	} else {
		_, _ = fmt.Fprintln(w, "  data:      unavailable")
	}
}

func renderInspectRelease(w io.Writer, release inspectReleaseSummary, unavailable bool) {
	if unavailable {
		_, _ = fmt.Fprintln(w, "  release:   unavailable")
		return
	}
	if !release.Available {
		_, _ = fmt.Fprintln(w, "  release:   no deployment")
		return
	}
	_, _ = fmt.Fprintf(w, "  release:   %s · %s · %s · %d%% traffic\n", release.DeploymentID, fallback(release.Status), fallback(release.Scope), release.TrafficPercent)
	if release.CanaryPreset != "none" {
		_, _ = fmt.Fprintf(w, "  safety:    %s canary · step %d/%d · %d health gates (%d firing)\n", release.CanaryPreset, release.CanaryStep, release.CanaryTotalSteps, release.HealthGateRules, release.FiringHealthGates)
	}
}

func renderInspectRecommendations(w io.Writer, recommendations []inspectRecommendation) {
	if len(recommendations) == 0 {
		_, _ = fmt.Fprintln(w, "\nRecommendations: none")
		return
	}
	_, _ = fmt.Fprintln(w, "\nRecommendations")
	for _, recommendation := range recommendations {
		_, _ = fmt.Fprintf(w, "  ! %s\n", recommendation.Message)
		if recommendation.Next != "" {
			_, _ = fmt.Fprintf(w, "    next: %s\n", recommendation.Next)
		}
	}
}

func fallback(value string) string {
	if value == "" {
		return GlyphEmDash
	}
	return value
}

func intOrDash(value int) string {
	if value == 0 {
		return GlyphEmDash
	}
	return fmt.Sprintf("%d", value)
}
