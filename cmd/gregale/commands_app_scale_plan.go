package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type appScalePlanChange struct {
	Field            string          `json:"field"`
	CurrentAvailable bool            `json:"current_available"`
	Current          json.RawMessage `json:"current"`
	Proposed         json.RawMessage `json:"proposed"`
}

type appScalePlanLimits struct {
	MaxMemoryMB         int  `json:"max_memory_mb"`
	MaxConcurrentVMs    int  `json:"max_concurrent_vms"`
	MinInstancesAllowed bool `json:"min_instances_allowed"`
	MaxMinInstances     int  `json:"max_min_instances"`
}

type appScaleResidentUsage struct {
	CurrentGBHours30d  float64 `json:"current_gb_hours_30d"`
	ProposedGBHours30d float64 `json:"proposed_gb_hours_30d"`
	ChangeGBHours30d   float64 `json:"change_gb_hours_30d"`
	Basis              string  `json:"basis"`
}

type appScalePlanView struct {
	SchemaVersion int                    `json:"schema_version"`
	AppSlug       string                 `json:"app_slug"`
	Environment   string                 `json:"environment,omitempty"`
	AccountPlan   string                 `json:"account_plan,omitempty"`
	PlanLimits    *appScalePlanLimits    `json:"plan_limits,omitempty"`
	Changes       []appScalePlanChange   `json:"changes"`
	Warnings      []string               `json:"warnings"`
	ResidentUsage *appScaleResidentUsage `json:"resident_usage,omitempty"`
	Notes         []string               `json:"notes"`
}

type appScaleSavedPlan struct {
	SchemaVersion        int                  `json:"schema_version"`
	AppSlug              string               `json:"app_slug"`
	Environment          string               `json:"environment,omitempty"`
	BaseConfigSHA256     string               `json:"base_config_sha256,omitempty"`
	BaseWorkloadRevision *int64               `json:"base_workload_revision,omitempty"`
	Request              api.UpdateAppRequest `json:"request"`
	Preview              appScalePlanView     `json:"preview"`
}

const appScaleSavedPlanMaxBytes = 1 << 20

var appScalePlanFieldLabels = map[string]string{
	"ram_mb": "RAM (MB)", "cpu_millicores": "CPU (millicores)", "resource_profile": "Resource profile",
	"max_concurrency": "Maximum concurrency", "scaling_policy": "Scaling policy",
	"idle_timeout_s": "Idle timeout (seconds)", "request_timeout_s": "Request timeout (seconds)",
	"min_instances": "Minimum warm instances", "autoscale_target_rps": "Autoscale RPS target",
	"autoscale_target_cpu_pct": "Autoscale CPU target", "warm_snapshot_enabled": "Warm snapshots",
	"warm_snapshot_min_requests": "Warm snapshot request threshold", "warm_snapshot_min_ms": "Warm snapshot delay (ms)",
	"warm_pool_size": "Warm pool size", "require_authn": "Request authentication",
	"public_auth": "Public URL authentication", "head_wakes": "HEAD request wakes",
	"crawler_policy": "Monitor and crawler policy", "health_path": "Health path",
	"health_path_wakes": "Health checks wake the app", "app_protocol": "App protocol",
}

func cmdAppScalePlan(client *Client, appClient environmentAppClient, slug, environment string, request api.UpdateAppRequest, outputPath string, expectedWorkloadRevision *int64) int {
	app, err := appClient.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Could not load current app settings", err)
	}
	if expectedWorkloadRevision != nil && (appClient.revision == nil || *appClient.revision != *expectedWorkloadRevision) {
		return printErr("Could not build app settings preview", errors.New("environment settings changed while building the preview; rerun the command"))
	}
	changes, err := buildAppScalePlanChanges(app, request)
	if err != nil {
		return printErr("Could not build app settings preview", err)
	}
	view := appScalePlanView{SchemaVersion: 1, AppSlug: slug, Environment: environment, Changes: changes, Warnings: []string{}, Notes: []string{
		"The API rechecks permissions, plan limits and configuration when you apply the settings.",
		"This preview changes no app settings. Remove --plan to apply the same flags.",
		"Resident usage excludes request-driven compute, egress and included plan usage; this is not a total bill estimate.",
	}}
	account, accountErr := client.Whoami(context.Background())
	if accountErr == nil {
		view.AccountPlan = account.Plan
		if limits, ok := api.LimitsFor(api.Plan(account.Plan)); ok {
			view.PlanLimits = &appScalePlanLimits{MaxMemoryMB: limits.RAMMB, MaxConcurrentVMs: limits.MaxConcurrency, MinInstancesAllowed: limits.MinInstancesAllowed, MaxMinInstances: limits.MaxMinInstances}
			view.Warnings = appScalePlanWarnings(app, request, api.Plan(account.Plan), limits)
			view.ResidentUsage = buildAppScaleResidentUsage(app, request, api.Plan(account.Plan), limits)
		} else {
			view.Notes = append(view.Notes, "The account plan is unknown; plan compatibility and resident usage cannot be estimated.")
		}
	} else {
		view.Notes = append(view.Notes, "The account plan could not be read; plan compatibility and resident usage cannot be estimated.")
	}
	var saved appScaleSavedPlan
	if outputPath != "" {
		saved = appScaleSavedPlan{SchemaVersion: 1, AppSlug: slug, Environment: environment, Request: request, Preview: view}
		if environment != "" {
			if appClient.revision == nil || *appClient.revision < 0 {
				return printErr("Could not save app settings plan", errors.New("the environment read did not return a workload revision"))
			}
			revision := *appClient.revision
			saved.BaseWorkloadRevision = &revision
		} else {
			saved.BaseConfigSHA256, err = appScaleCurrentConfigHash(app)
			if err != nil {
				return printErr("Could not save app settings plan", err)
			}
		}
		if err := writeAppScaleSavedPlan(outputPath, saved); err != nil {
			return printErr("Could not save app settings plan", err)
		}
	}
	if jsonOutput {
		if outputPath != "" {
			return jsonOut(writeJSON(saved))
		}
		return jsonOut(writeJSON(view))
	}
	printAppScalePlan(osStdout, view)
	if outputPath != "" {
		PrintOK(osStdout, "Saved reusable plan to %s", outputPath)
	}
	return 0
}

func buildAppScalePlanChanges(app api.AppResponse, request api.UpdateAppRequest) ([]appScalePlanChange, error) {
	current, err := appScaleCurrentValues(app)
	if err != nil {
		return nil, err
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var proposed map[string]json.RawMessage
	if err := json.Unmarshal(requestJSON, &proposed); err != nil {
		return nil, err
	}
	fields := make([]string, 0, len(proposed))
	for field := range proposed {
		if _, ok := appScalePlanFieldLabels[field]; !ok {
			return nil, fmt.Errorf("no preview label for changed field %q", field)
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)
	changes := make([]appScalePlanChange, 0, len(fields))
	for _, field := range fields {
		value, exists := current[field]
		changes = append(changes, appScalePlanChange{Field: field, CurrentAvailable: exists, Current: value, Proposed: proposed[field]})
	}
	return changes, nil
}

func appScaleCurrentValues(app api.AppResponse) (map[string]json.RawMessage, error) {
	currentJSON, err := json.Marshal(app)
	if err != nil {
		return nil, err
	}
	manifestJSON, err := json.Marshal(app.Manifest)
	if err != nil {
		return nil, err
	}
	var current, manifest map[string]json.RawMessage
	if err := json.Unmarshal(currentJSON, &current); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, err
	}
	nestedManifestFields := map[string]bool{"head_wakes": true, "crawler_policy": true, "health_path": true, "health_path_wakes": true}
	for field := range appScalePlanFieldLabels {
		if _, ok := current[field]; !ok && nestedManifestFields[field] {
			if value, exists := manifest[field]; exists {
				current[field] = value
			}
		}
		if _, ok := current[field]; !ok {
			var fallback any
			switch field {
			case "idle_timeout_s":
				fallback = app.IdleTimeoutS
			case "request_timeout_s":
				fallback = app.RequestTimeoutS
			case "scaling_policy":
				fallback = app.ScalingPolicy
			case "resource_profile":
				fallback = app.ResourceProfile
			case "head_wakes":
				fallback = app.Manifest.HeadWakes
			case "crawler_policy":
				fallback = app.Manifest.EffectiveCrawlerPolicy()
			case "health_path":
				fallback = app.Manifest.HealthPath
			case "health_path_wakes":
				fallback = app.Manifest.HealthPathWakes
			default:
				return nil, fmt.Errorf("current setting %q was not returned by the server", field)
			}
			current[field], err = json.Marshal(fallback)
			if err != nil {
				return nil, err
			}
		}
	}
	values := make(map[string]json.RawMessage, len(appScalePlanFieldLabels))
	for field := range appScalePlanFieldLabels {
		values[field] = current[field]
	}
	return values, nil
}

func appScaleCurrentConfigHash(app api.AppResponse) (string, error) {
	values, err := appScaleCurrentValues(app)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func appScaleHasSettingFlags(explicit map[string]bool) bool {
	for name, set := range explicit {
		if !set {
			continue
		}
		switch name {
		case "plan", "out", "apply", "confirm", "environment":
			continue
		default:
			return true
		}
	}
	return false
}

func writeAppScaleSavedPlan(path string, plan appScaleSavedPlan) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve plan path: %w", err)
	}
	body, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	body = append(body, '\n')
	file, err := os.CreateTemp(filepath.Dir(absPath), ".gregale-app-scale-plan-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary plan: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if _, err := file.Write(body); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync plan: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close plan: %w", err)
	}
	if err := os.Link(file.Name(), absPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("choose a new path; %q already exists and will not be replaced", path)
		}
		return fmt.Errorf("publish plan to a new file: %w", err)
	}
	return nil
}

func readAppScaleSavedPlan(path string) (appScaleSavedPlan, error) {
	var plan appScaleSavedPlan
	file, err := openCustomerFile(path)
	if err != nil {
		return plan, fmt.Errorf("open plan file: %w", err)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, appScaleSavedPlanMaxBytes+1))
	if err != nil {
		return plan, fmt.Errorf("read plan file: %w", err)
	}
	if len(body) > appScaleSavedPlanMaxBytes {
		return plan, fmt.Errorf("plan file exceeds %d bytes", appScaleSavedPlanMaxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return plan, fmt.Errorf("decode plan file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return plan, errors.New("plan file must contain exactly one JSON object")
	}
	return plan, nil
}

func cmdAppScaleApplyPlan(slug, path string) int {
	plan, err := readAppScaleSavedPlan(path)
	if err != nil {
		return printErr("Could not read saved app settings plan", err)
	}
	if plan.SchemaVersion != 1 || plan.AppSlug != slug || plan.Preview.SchemaVersion != 1 || plan.Preview.AppSlug != slug || plan.Preview.Environment != plan.Environment {
		return printErr("Invalid saved app settings plan", errors.New("schema, app slug, or environment does not match the plan file"))
	}
	if plan.Environment == "" {
		if len(plan.BaseConfigSHA256) != sha256.Size*2 || plan.BaseWorkloadRevision != nil {
			return printErr("Invalid saved app settings plan", errors.New("standalone plans require a base configuration hash only"))
		}
		if _, err := hex.DecodeString(plan.BaseConfigSHA256); err != nil {
			return printErr("Invalid saved app settings plan", errors.New("base configuration hash is malformed"))
		}
	} else if plan.BaseWorkloadRevision == nil || *plan.BaseWorkloadRevision < 0 || plan.BaseConfigSHA256 != "" {
		return printErr("Invalid saved app settings plan", errors.New("environment plans require a workload revision only"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	revision := int64(-1)
	if plan.BaseWorkloadRevision != nil {
		revision = *plan.BaseWorkloadRevision
	}
	appClient := environmentAppClient{Client: client, environment: plan.Environment, revision: &revision}
	current, err := appClient.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Could not verify saved app settings plan", err)
	}
	if plan.Environment != "" {
		if revision != *plan.BaseWorkloadRevision {
			return printErr("Saved plan is stale", errors.New("the environment settings changed since this plan was created; make a fresh --plan"))
		}
	} else {
		currentHash, err := appScaleCurrentConfigHash(current)
		if err != nil {
			return printErr("Could not verify saved app settings plan", err)
		}
		if currentHash != plan.BaseConfigSHA256 {
			return printErr("Saved plan is stale", errors.New("the app scale settings changed since this plan was created; make a fresh --plan"))
		}
	}
	if _, err := buildAppScalePlanChanges(current, plan.Request); err != nil {
		return printErr("Invalid saved app settings plan", err)
	}
	requestJSON, err := json.Marshal(plan.Request)
	if err != nil {
		return printErr("Invalid saved app settings plan", err)
	}
	var requestFields map[string]json.RawMessage
	if err := json.Unmarshal(requestJSON, &requestFields); err != nil || len(requestFields) == 0 {
		return printErr("Invalid saved app settings plan", errors.New("plan does not contain any settings to apply"))
	}
	if !jsonOutput {
		printAppScalePlan(osStdout, plan.Preview)
		PrintProgress(osStdout, "Applying the confirmed plan.")
	}
	updated, err := appClient.UpdateApp(context.Background(), slug, plan.Request)
	if err != nil {
		return printErr("Could not apply saved app settings plan", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	PrintOK(osStdout, "Applied saved scale plan for %s", slug)
	if plan.Request.MinInstances != nil && *plan.Request.MinInstances > 0 {
		if account, err := client.Whoami(context.Background()); err == nil {
			printResidentCostEcho(api.Plan(account.Plan), updated.RAMMB, *plan.Request.MinInstances)
		}
	}
	return 0
}

func appScalePlanWarnings(app api.AppResponse, request api.UpdateAppRequest, plan api.Plan, limits api.Limits) []string {
	var warnings []string
	maxVMs := app.MaxConcurrency
	if request.MaxConcurrency != nil {
		maxVMs = *request.MaxConcurrency
	}
	if request.ScalingPolicy != nil && request.ScalingPolicy.MaxInstances > 0 {
		maxVMs = request.ScalingPolicy.MaxInstances
	}
	minInstances := app.MinInstances
	if app.ScalingPolicy != nil && app.ScalingPolicy.MinInstances > minInstances {
		minInstances = app.ScalingPolicy.MinInstances
	}
	if request.ScalingPolicy != nil {
		minInstances = request.ScalingPolicy.MinInstances
	}
	if request.MinInstances != nil {
		minInstances = *request.MinInstances
	}
	if maxVMs < 1 {
		warnings = append(warnings, "Maximum concurrency must be positive.")
	}
	if maxVMs > limits.MaxConcurrency {
		warnings = append(warnings, fmt.Sprintf("Requested maximum concurrency %d exceeds the plan limit of %d.", maxVMs, limits.MaxConcurrency))
	}
	proposedRAM := appScalePlanCurrentRAM(app)
	if request.ResourceProfile != nil {
		if shape, ok := api.ResourceProfileSpecFor(*request.ResourceProfile); ok {
			proposedRAM = shape.MemoryMB
			if shape.MemoryMB > limits.RAMMB {
				warnings = append(warnings, fmt.Sprintf("The %s profile's RAM %d MB exceeds the plan limit of %d MB.", *request.ResourceProfile, shape.MemoryMB, limits.RAMMB))
			}
			if request.RAMMB != nil && *request.RAMMB != shape.MemoryMB {
				warnings = append(warnings, fmt.Sprintf("Requested RAM %d MB conflicts with the %s profile's %d MB shape.", *request.RAMMB, *request.ResourceProfile, shape.MemoryMB))
			}
			if request.CPUMillicores != nil && *request.CPUMillicores != shape.CPUMillicores {
				warnings = append(warnings, fmt.Sprintf("Requested CPU %d millicores conflicts with the %s profile's %d millicore shape.", *request.CPUMillicores, *request.ResourceProfile, shape.CPUMillicores))
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("Unknown resource profile %q.", *request.ResourceProfile))
		}
	}
	if request.RAMMB != nil {
		proposedRAM = *request.RAMMB
	}
	if proposedRAM > limits.RAMMB {
		warnings = append(warnings, fmt.Sprintf("Requested RAM %d MB exceeds the plan limit of %d MB.", proposedRAM, limits.RAMMB))
	}
	if request.RAMMB != nil && *request.RAMMB < 1 {
		warnings = append(warnings, "RAM must be positive.")
	}
	if request.CPUMillicores != nil && !api.ValidAppCPUMillicores(*request.CPUMillicores) {
		warnings = append(warnings, "CPU must be 250, 500 or 1000 millicores.")
	}
	if request.ScalingPolicy != nil && request.ScalingPolicy.MaxInstances > 0 && !limits.MaxInstancesAllowed {
		warnings = append(warnings, "This plan does not allow setting a maximum instance count.")
	}
	if request.ScalingPolicy != nil {
		policy := request.ScalingPolicy
		if policy.MaxQueueDepth > api.ConcurrencyQueueMaxDepthForPlan(plan) {
			warnings = append(warnings, fmt.Sprintf("Warm queue depth exceeds this plan's limit of %d.", api.ConcurrencyQueueMaxDepthForPlan(plan)))
		}
		if policy.WakeMaxQueueDepth > api.WakeQueueMaxDepthForPlan(plan) {
			warnings = append(warnings, fmt.Sprintf("Cold-wake queue depth exceeds this plan's limit of %d.", api.WakeQueueMaxDepthForPlan(plan)))
		}
		if policy.WakeMaxQueueWaitSeconds > api.WakeQueueMaxWaitSeconds {
			warnings = append(warnings, fmt.Sprintf("Cold-wake queue wait exceeds the limit of %d seconds.", api.WakeQueueMaxWaitSeconds))
		}
	}
	if minInstances > 0 && !limits.MinInstancesAllowed {
		warnings = append(warnings, "This plan does not allow keeping a minimum number of instances warm.")
	}
	if minInstances < 0 {
		warnings = append(warnings, "Minimum instances cannot be negative.")
	}
	if minInstances > limits.MaxMinInstances {
		warnings = append(warnings, fmt.Sprintf("Requested minimum instances %d exceeds the plan limit of %d.", minInstances, limits.MaxMinInstances))
	}
	if minInstances > maxVMs {
		warnings = append(warnings, fmt.Sprintf("Requested minimum instances %d exceeds the app's maximum concurrency of %d.", minInstances, maxVMs))
	}
	if request.AutoscaleTargetRPS != nil && !limits.ScaleUpTargetRPSAllowed {
		warnings = append(warnings, "This plan does not allow autoscaling by request rate.")
	}
	if request.AutoscaleTargetRPS != nil && *request.AutoscaleTargetRPS < 0 {
		warnings = append(warnings, "Autoscale RPS target cannot be negative.")
	}
	if request.AutoscaleTargetCPUPct != nil && !limits.ScaleUpTargetCPUAllowed {
		warnings = append(warnings, "This plan does not allow autoscaling by CPU usage.")
	}
	if request.AutoscaleTargetCPUPct != nil && *request.AutoscaleTargetCPUPct != 0 && (*request.AutoscaleTargetCPUPct < 1 || *request.AutoscaleTargetCPUPct > 100) {
		warnings = append(warnings, "Autoscale CPU target must be between 1 and 100 percent, or 0 to disable.")
	}
	if request.WarmSnapshotEnabled != nil && *request.WarmSnapshotEnabled && !limits.WarmSnapshotEnabled {
		warnings = append(warnings, "This plan does not allow warm snapshots.")
	}
	if request.WarmPoolSize != nil {
		if *request.WarmPoolSize > 0 && !limits.WarmPoolAllowed {
			warnings = append(warnings, "This plan does not allow a paused warm pool.")
		}
		if *request.WarmPoolSize < 0 || *request.WarmPoolSize > maxVMs {
			warnings = append(warnings, fmt.Sprintf("Warm pool size must be between 0 and the app's maximum concurrency (%d).", maxVMs))
		}
	}
	if request.RequireAuthn != nil && *request.RequireAuthn && !limits.RequireAuthn {
		warnings = append(warnings, "This plan does not allow requiring authentication on the public app URL.")
	}
	if request.HealthPathWakes != nil && *request.HealthPathWakes && !plan.HealthPathWakesAllowed() {
		warnings = append(warnings, "This plan does not allow health checks to wake the app.")
	}
	if request.AppProtocol != nil && *request.AppProtocol == api.AppProtocolGRPC && !plan.AppProtocolAllowed(api.AppProtocolGRPC) {
		warnings = append(warnings, "This plan does not allow the gRPC app protocol.")
	}
	if request.PublicAuth != nil {
		switch request.PublicAuth.Mode {
		case api.AppPublicAuthModeBearer, api.AppPublicAuthModeBasic:
			if !plan.PublicAuthBearerAllowed() {
				warnings = append(warnings, "This plan does not allow bearer or basic authentication on the public app URL.")
			}
		case api.AppPublicAuthModeIPAllowlist:
			if !limits.PublicAuthIPAllowlistAllowed {
				warnings = append(warnings, "This plan does not allow an IP allowlist on the public app URL.")
			} else if len(request.PublicAuth.IPAllowlist) > limits.PublicAuthIPAllowlistMaxEntries {
				warnings = append(warnings, fmt.Sprintf("The IP allowlist has %d entries; this plan allows up to %d.", len(request.PublicAuth.IPAllowlist), limits.PublicAuthIPAllowlistMaxEntries))
			}
		}
	}
	return warnings
}

func buildAppScaleResidentUsage(app api.AppResponse, request api.UpdateAppRequest, plan api.Plan, limits api.Limits) *appScaleResidentUsage {
	if !appScaleResidentEstimateAllowed(app, request, limits) {
		return nil
	}
	currentMin, proposedMin := appScalePlanCurrentMin(app), appScalePlanCurrentMin(app)
	if request.ScalingPolicy != nil {
		proposedMin = request.ScalingPolicy.MinInstances
	}
	if request.MinInstances != nil {
		proposedMin = *request.MinInstances
	}
	if currentMin <= 0 && proposedMin <= 0 {
		return nil
	}
	currentRAM := appScalePlanCurrentRAM(app)
	proposedRAM := currentRAM
	if profile := request.ResourceProfile; profile != nil {
		shape, _ := api.ResourceProfileSpecFor(*profile)
		proposedRAM = shape.MemoryMB
	}
	if request.RAMMB != nil {
		proposedRAM = *request.RAMMB
	}
	currentGBHours := ResidentGBHoursPerMonth(plan, currentRAM, currentMin)
	proposedGBHours := ResidentGBHoursPerMonth(plan, proposedRAM, proposedMin)
	return &appScaleResidentUsage{CurrentGBHours30d: currentGBHours, ProposedGBHours30d: proposedGBHours, ChangeGBHours30d: proposedGBHours - currentGBHours, Basis: "Always-resident instances only; 30 days. Excludes request-driven usage, egress and plan allowances."}
}

func appScaleResidentEstimateAllowed(app api.AppResponse, request api.UpdateAppRequest, limits api.Limits) bool {
	maxVMs := app.MaxConcurrency
	if request.MaxConcurrency != nil {
		maxVMs = *request.MaxConcurrency
	}
	if request.ScalingPolicy != nil && request.ScalingPolicy.MaxInstances > 0 {
		maxVMs = request.ScalingPolicy.MaxInstances
	}
	minVMs := appScalePlanCurrentMin(app)
	if request.ScalingPolicy != nil {
		minVMs = request.ScalingPolicy.MinInstances
	}
	if request.MinInstances != nil {
		minVMs = *request.MinInstances
	}
	if minVMs < 0 || (minVMs > 0 && (!limits.MinInstancesAllowed || minVMs > limits.MaxMinInstances || minVMs > maxVMs)) {
		return false
	}
	if maxVMs < 1 || maxVMs > limits.MaxConcurrency {
		return false
	}
	ram := appScalePlanCurrentRAM(app)
	if request.ResourceProfile != nil {
		shape, ok := api.ResourceProfileSpecFor(*request.ResourceProfile)
		if !ok || (request.RAMMB != nil && *request.RAMMB != shape.MemoryMB) || (request.CPUMillicores != nil && *request.CPUMillicores != shape.CPUMillicores) {
			return false
		}
		ram = shape.MemoryMB
	}
	if request.RAMMB != nil {
		ram = *request.RAMMB
	}
	return ram > 0 && ram <= limits.RAMMB
}

func appScalePlanCurrentRAM(app api.AppResponse) int {
	if app.ConfiguredResources.MemoryMB > 0 {
		return app.ConfiguredResources.MemoryMB
	}
	return app.RAMMB
}

func appScalePlanCurrentMin(app api.AppResponse) int {
	if app.MinInstances > 0 {
		return app.MinInstances
	}
	if app.ScalingPolicy != nil {
		return app.ScalingPolicy.MinInstances
	}
	return app.MinInstances
}

func printAppScalePlan(w io.Writer, plan appScalePlanView) {
	_, _ = fmt.Fprintf(w, "App settings preview for %s (no changes applied)\n", plan.AppSlug)
	if plan.Environment != "" {
		_, _ = fmt.Fprintf(w, "  environment: %s\n", plan.Environment)
	}
	if plan.AccountPlan != "" {
		_, _ = fmt.Fprintf(w, "  account plan: %s\n", plan.AccountPlan)
	}
	_, _ = fmt.Fprintln(w, "  changes:")
	for _, change := range plan.Changes {
		before := "unknown (not returned by the server)"
		if change.CurrentAvailable {
			before = appScalePlanFormatValue(change.Current)
		}
		_, _ = fmt.Fprintf(w, "    %s: %s → %s\n", appScalePlanFieldLabels[change.Field], before, appScalePlanFormatValue(change.Proposed))
	}
	if plan.PlanLimits != nil {
		_, _ = fmt.Fprintf(w, "  plan limits: RAM %d MB · max concurrent instances %d · min warm instances up to %d\n", plan.PlanLimits.MaxMemoryMB, plan.PlanLimits.MaxConcurrentVMs, plan.PlanLimits.MaxMinInstances)
	}
	for _, warning := range plan.Warnings {
		_, _ = fmt.Fprintf(w, "  check: %s\n", warning)
	}
	if plan.ResidentUsage != nil {
		_, _ = fmt.Fprintf(w, "  always-resident usage: ~%.1f → ~%.1f GB-h/30d (change %+.1f GB-h/30d)\n", plan.ResidentUsage.CurrentGBHours30d, plan.ResidentUsage.ProposedGBHours30d, plan.ResidentUsage.ChangeGBHours30d)
		_, _ = fmt.Fprintf(w, "    %s\n", plan.ResidentUsage.Basis)
	}
	for _, note := range plan.Notes {
		_, _ = fmt.Fprintf(w, "  note: %s\n", note)
	}
}

func appScalePlanFormatValue(value json.RawMessage) string {
	if len(value) == 0 {
		return "null"
	}
	var compact bytes.Buffer
	if json.Compact(&compact, value) == nil {
		return compact.String()
	}
	return string(value)
}
