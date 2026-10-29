package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	mcpTasksObserverDatabaseEnvDefault = "DATABASE_URL"
	mcpTasksObserverOwnerKeyEnvDefault = "MCP_TASK_OWNER_KEY"
	mcpTasksObserverNamespaceEnv       = "MCP_TASK_NAMESPACE"
	mcpTasksObserverMetricsSlugEnv     = "MCP_TASKS_SCALING_APP_SLUG"
	mcpTasksObserverMetricsTokenEnv    = "MCP_TASKS_SCALING_TOKEN"
)

var mcpTasksObserverEnvNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

type mcpTasksObserverSourceConfig struct {
	Tasks *struct {
		Enabled        bool   `json:"enabled"`
		DatabaseURLEnv string `json:"database_url_env"`
		OwnerKeyEnv    string `json:"owner_key_env"`
	} `json:"tasks"`
}

type mcpTasksObserverCredentialCheck struct {
	Env            string `json:"env"`
	Configured     bool   `json:"configured"`
	SecretBacked   bool   `json:"secret_backed"`
	SecretExists   bool   `json:"secret_exists"`
	MetadataLoaded bool   `json:"secret_metadata_loaded"`
	Ready          bool   `json:"ready"`
}

type mcpTasksObserverSetupResult struct {
	ObserverApp                   mcpTasksObserverReadiness       `json:"observer_app"`
	ObserverSlug                  string                          `json:"observer_app_slug"`
	WorkerSlug                    string                          `json:"worker_app_slug"`
	ObserverProcessDetected       bool                            `json:"observer_process_detected"`
	TasksConfigLoaded             bool                            `json:"tasks_config_loaded"`
	TasksEnabled                  bool                            `json:"tasks_enabled"`
	DatabaseEnv                   string                          `json:"database_env"`
	OwnerKeyEnv                   string                          `json:"owner_key_env"`
	ObserverDatabaseCredential    mcpTasksObserverCredentialCheck `json:"observer_database_credential"`
	WorkerDatabaseCredential      mcpTasksObserverCredentialCheck `json:"worker_database_credential"`
	WorkerOwnerKeyCredential      mcpTasksObserverCredentialCheck `json:"worker_owner_key_credential"`
	ObserverMetricsToken          mcpTasksObserverCredentialCheck `json:"observer_metrics_token"`
	WorkerMetricsToken            mcpTasksObserverCredentialCheck `json:"worker_metrics_token"`
	ObserverMetricsTargetMatches  bool                            `json:"observer_metrics_target_matches_worker"`
	WorkerMetricsTargetMatches    bool                            `json:"worker_metrics_target_matches_worker"`
	NamespaceMatches              bool                            `json:"namespace_matches"`
	ObserverMinimumConfigured     bool                            `json:"observer_minimum_configured"`
	ObserverHeartbeatFresh        bool                            `json:"observer_heartbeat_fresh"`
	ReadyToLink                   bool                            `json:"ready_to_link"`
	EligibleToApplyMinimum        bool                            `json:"eligible_to_apply_minimum"`
	MinimumChanged                bool                            `json:"minimum_changed"`
	ReadOnlyDatabaseGrantVerified bool                            `json:"read_only_database_grant_verified"`
	MetricsTokenScopeVerified     bool                            `json:"metrics_token_scope_verified"`
	ManualChecks                  []string                        `json:"manual_checks"`
	Violations                    []string                        `json:"violations"`
}

func cmdMCPTasksObserver(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr,
			"usage: gregale mcp server tasks observer <setup|status> --app <observer-app> --worker-app <worker-app> [flags]", "mcp")
		return 0
	}
	switch args[0] {
	case "setup":
		return cmdMCPTasksObserverSetup(args[1:])
	case "status":
		return cmdMCPTasksObserverStatus(args[1:])
	default:
		return printErr("Unknown MCP task observer command", fmt.Errorf("%q", args[0]))
	}
}

func cmdMCPTasksObserverSetup(args []string) int {
	fs := newFlagSet("mcp-tasks-observer-setup", flag.ContinueOnError)
	observerFlag := fs.String("app", "", "always-on observer worker app slug")
	workerFlag := fs.String("worker-app", "", "task worker app slug whose metrics the observer publishes")
	configFlag := fs.String("config", "gregale-mcp.json", "local MCP source config used by the observer")
	apply := fs.Bool("apply", false, "ensure the observer worker has at least one configured replica")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return printErr("Invalid MCP task observer setup flags", errors.New("unexpected positional arguments"))
	}
	if !api.ValidAppSlug(*observerFlag) || !api.ValidAppSlug(*workerFlag) || *observerFlag == *workerFlag {
		return printErr("Invalid MCP task observer apps", errors.New("--app and --worker-app must be valid, distinct Gregale app slugs"))
	}
	config, err := readMCPTasksObserverSourceConfig(*configFlag)
	if err != nil {
		return printErr("Could not read MCP task observer config", err)
	}
	if config.Tasks == nil || !config.Tasks.Enabled {
		return printErr("MCP Tasks are not enabled", errors.New("enable tasks in the local gregale-mcp.json before setting up the task observer"))
	}
	databaseEnv, ownerKeyEnv, err := mcpTasksObserverConfigEnvNames(config)
	if err != nil {
		return printErr("Invalid MCP task observer config", err)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	worker, err := client.GetApp(ctx, *workerFlag)
	if err != nil {
		return printErr("Could not get MCP task worker app", err)
	}
	observer, err := client.GetApp(ctx, *observerFlag)
	if err != nil {
		return printErr("Could not get MCP task observer app", err)
	}
	if !isWorkerApp(worker) || !isWorkerApp(observer) {
		return printErr("Invalid MCP task app mode", errors.New("the task worker and observer must both be worker-mode apps"))
	}
	result, err := inspectMCPTasksObserver(ctx, client, worker, observer, databaseEnv, ownerKeyEnv)
	if err != nil {
		return printErr("Could not inspect MCP task observer readiness", err)
	}
	result.TasksConfigLoaded = true
	result.TasksEnabled = true
	if *apply {
		if !result.EligibleToApplyMinimum {
			if jsonOutput {
				if code := jsonOut(writeJSON(result)); code != 0 {
					return code
				}
			} else {
				printMCPTasksObserverSetup(osStdout, result, false)
			}
			return 1
		}
		result.MinimumChanged, err = applyMCPTasksObserverMinimum(ctx, client, observer)
		if err != nil {
			return printErr("Could not ensure an always-on MCP task observer", err)
		}
		if result.MinimumChanged {
			observer, err = client.GetApp(ctx, observer.Slug)
			if err != nil {
				return printErr("Could not refresh MCP task observer", err)
			}
			result, err = inspectMCPTasksObserver(ctx, client, worker, observer, databaseEnv, ownerKeyEnv)
			if err != nil {
				return printErr("Could not refresh MCP task observer readiness", err)
			}
			result.MinimumChanged = true
			result.TasksConfigLoaded = true
			result.TasksEnabled = true
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	printMCPTasksObserverSetup(osStdout, result, *apply)
	if *apply && !result.EligibleToApplyMinimum {
		return 1
	}
	return 0
}

func cmdMCPTasksObserverStatus(args []string) int {
	fs := newFlagSet("mcp-tasks-observer-status", flag.ContinueOnError)
	observerFlag := fs.String("app", "", "always-on observer worker app slug")
	workerFlag := fs.String("worker-app", "", "task worker app slug whose metrics the observer publishes")
	configFlag := fs.String("config", "", "optional local MCP config used to discover custom task credential env names")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return printErr("Invalid MCP task observer status flags", errors.New("unexpected positional arguments"))
	}
	if !api.ValidAppSlug(*workerFlag) {
		return printErr("Invalid MCP task worker app", errors.New("--worker-app must be a valid Gregale app slug"))
	}
	if *observerFlag != "" && (!api.ValidAppSlug(*observerFlag) || *observerFlag == *workerFlag) {
		return printErr("Invalid MCP task observer app", errors.New("--app must be a valid slug distinct from --worker-app"))
	}
	databaseEnv, ownerKeyEnv := mcpTasksObserverDatabaseEnvDefault, mcpTasksObserverOwnerKeyEnvDefault
	if *configFlag != "" {
		config, err := readMCPTasksObserverSourceConfig(*configFlag)
		if err != nil {
			return printErr("Could not read MCP task observer config", err)
		}
		if config.Tasks == nil || !config.Tasks.Enabled {
			return printErr("MCP Tasks are not enabled", errors.New("the local MCP config does not enable tasks"))
		}
		databaseEnv, ownerKeyEnv, err = mcpTasksObserverConfigEnvNames(config)
		if err != nil {
			return printErr("Invalid MCP task observer config", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	worker, err := client.GetApp(ctx, *workerFlag)
	if err != nil {
		return printErr("Could not get MCP task worker app", err)
	}
	observerSlug := *observerFlag
	if observerSlug == "" && worker.Manifest.WorkerReplicas != nil {
		observerSlug = worker.Manifest.WorkerReplicas.ObserverAppSlug
	}
	if observerSlug == "" {
		return printErr("MCP task observer is not linked", errors.New("pass --app <observer-app>; the worker has no linked observer app"))
	}
	observer, err := client.GetApp(ctx, observerSlug)
	if err != nil {
		return printErr("Could not get MCP task observer app", err)
	}
	if !isWorkerApp(worker) || !isWorkerApp(observer) {
		return printErr("Invalid MCP task app mode", errors.New("the task worker and observer must both be worker-mode apps"))
	}
	result, err := inspectMCPTasksObserver(ctx, client, worker, observer, databaseEnv, ownerKeyEnv)
	if err != nil {
		return printErr("Could not inspect MCP task observer status", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		printMCPTasksObserverSetup(osStdout, result, false)
	}
	if !result.ReadyToLink {
		return 1
	}
	return 0
}

func readMCPTasksObserverSourceConfig(path string) (mcpTasksObserverSourceConfig, error) {
	var config mcpTasksObserverSourceConfig
	if strings.TrimSpace(path) == "" {
		return config, errors.New("config path is required")
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return config, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return config, err
	}
	if len(body) > 1<<20 {
		return config, errors.New("config file exceeds 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("config must be valid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return config, errors.New("config must contain one JSON object")
	}
	return config, nil
}

func mcpTasksObserverConfigEnvNames(config mcpTasksObserverSourceConfig) (string, string, error) {
	if config.Tasks == nil {
		return "", "", errors.New("tasks config is missing")
	}
	databaseEnv := config.Tasks.DatabaseURLEnv
	if databaseEnv == "" {
		databaseEnv = mcpTasksObserverDatabaseEnvDefault
	}
	ownerKeyEnv := config.Tasks.OwnerKeyEnv
	if ownerKeyEnv == "" {
		ownerKeyEnv = mcpTasksObserverOwnerKeyEnvDefault
	}
	if !mcpTasksObserverEnvNamePattern.MatchString(databaseEnv) || !mcpTasksObserverEnvNamePattern.MatchString(ownerKeyEnv) {
		return "", "", errors.New("database_url_env and owner_key_env must be valid uppercase environment variable names")
	}
	return databaseEnv, ownerKeyEnv, nil
}

func inspectMCPTasksObserver(ctx context.Context, client *api.Client, worker, observer api.AppResponse, databaseEnv, ownerKeyEnv string) (mcpTasksObserverSetupResult, error) {
	result := mcpTasksObserverSetupResult{
		ObserverSlug:                  observer.Slug,
		WorkerSlug:                    worker.Slug,
		DatabaseEnv:                   databaseEnv,
		OwnerKeyEnv:                   ownerKeyEnv,
		ReadOnlyDatabaseGrantVerified: false,
		MetricsTokenScopeVerified:     false,
		ManualChecks: []string{
			"Verify the observer database role has SELECT-only access to the task tables; Gregale cannot inspect PostgreSQL grants.",
			"Verify the MCP_TASKS_SCALING_TOKEN API key is restricted to metrics:write; secret values and key scopes are not exposed to this command.",
		},
		Violations: []string{},
	}
	result.ObserverProcessDetected = mcpTasksObserverEntrypointDetected(observer.Manifest.Entrypoint) ||
		mcpTasksObserverRoleConfigured(observer.Manifest.Env, observer.Manifest.Entrypoint)
	result.ObserverMinimumConfigured = observer.Manifest.WorkerReplicas != nil && observer.Manifest.WorkerReplicas.Min >= 1
	result.NamespaceMatches = mcpTasksObserverNamespacesMatch(worker, observer)
	result.ObserverMetricsTargetMatches = mcpTasksObserverMetricsTargetMatches(observer, worker.Slug)
	result.WorkerMetricsTargetMatches = mcpTasksObserverMetricsTargetMatches(worker, worker.Slug)

	observerSecrets, observerSecretsErr := client.ListSecretsWithScope(ctx, observer.Slug, "__all__")
	workerSecrets, workerSecretsErr := client.ListSecretsWithScope(ctx, worker.Slug, "__all__")
	result.ObserverDatabaseCredential = mcpTasksObserverCredential(observer, databaseEnv, observerSecrets, observerSecretsErr == nil)
	result.WorkerDatabaseCredential = mcpTasksObserverCredential(worker, databaseEnv, workerSecrets, workerSecretsErr == nil)
	result.WorkerOwnerKeyCredential = mcpTasksObserverCredential(worker, ownerKeyEnv, workerSecrets, workerSecretsErr == nil)
	result.ObserverMetricsToken = mcpTasksObserverCredential(observer, mcpTasksObserverMetricsTokenEnv, observerSecrets, observerSecretsErr == nil)
	result.WorkerMetricsToken = mcpTasksObserverCredential(worker, mcpTasksObserverMetricsTokenEnv, workerSecrets, workerSecretsErr == nil)

	result.ObserverApp = loadMCPTasksObserverReadiness(ctx, client, worker, observer.Slug)
	metrics, err := client.GetAppCustomMetrics(ctx, worker.Slug)
	if err != nil {
		return result, err
	}
	for _, metric := range metrics.Metrics {
		if metric.Name == mcpTasksObserverHeartbeatMetric && !metric.Stale && metric.Value == 1 {
			result.ObserverHeartbeatFresh = true
			break
		}
	}

	observerAndWorkerUsable := mcpTasksObserverAppUsable(worker) && mcpTasksObserverAppUsable(observer) && worker.ID != observer.ID
	result.EligibleToApplyMinimum = observerAndWorkerUsable && result.ObserverProcessDetected &&
		result.ObserverDatabaseCredential.Ready && result.WorkerDatabaseCredential.Ready &&
		result.WorkerOwnerKeyCredential.Ready && result.ObserverMetricsToken.Ready &&
		result.WorkerMetricsToken.Ready && result.ObserverMetricsTargetMatches &&
		result.WorkerMetricsTargetMatches && result.NamespaceMatches
	result.ReadyToLink = result.EligibleToApplyMinimum && result.ObserverMinimumConfigured &&
		result.ObserverApp.Ready && result.ObserverHeartbeatFresh

	if !observerAndWorkerUsable {
		result.Violations = append(result.Violations, "worker_and_observer_must_be_distinct_active_worker_apps_with_live_deployments")
	}
	if !result.ObserverProcessDetected {
		result.Violations = append(result.Violations, "observer_entrypoint_not_detected_use_the_tasks_observer_start_command")
	}
	if !result.ObserverDatabaseCredential.Ready {
		result.Violations = append(result.Violations, "observer_database_url_must_be_bound_to_an_existing_secret")
	}
	if !result.WorkerDatabaseCredential.Ready {
		result.Violations = append(result.Violations, "worker_database_url_must_be_bound_to_an_existing_secret")
	}
	if !result.WorkerOwnerKeyCredential.Ready {
		result.Violations = append(result.Violations, "worker_task_owner_key_must_be_bound_to_an_existing_secret")
	}
	if !result.ObserverMetricsToken.Ready {
		result.Violations = append(result.Violations, "observer_metrics_token_must_be_bound_to_an_existing_secret")
	}
	if !result.WorkerMetricsToken.Ready {
		result.Violations = append(result.Violations, "worker_metrics_token_must_be_bound_to_an_existing_secret")
	}
	if !result.ObserverMetricsTargetMatches || !result.WorkerMetricsTargetMatches {
		result.Violations = append(result.Violations, "both_metrics_publishers_must_target_the_worker_app_slug")
	}
	if !result.NamespaceMatches {
		result.Violations = append(result.Violations, "worker_and_observer_must_have_the_same_explicit_mcp_task_namespace")
	}
	if !result.ObserverMinimumConfigured {
		result.Violations = append(result.Violations, "observer_worker_minimum_must_be_at_least_one")
	}
	if !result.ObserverApp.Ready {
		result.Violations = append(result.Violations, result.ObserverApp.Violations...)
	}
	if !result.ObserverHeartbeatFresh {
		result.Violations = append(result.Violations, "observer_heartbeat_is_not_fresh_on_the_worker_app")
	}
	if observer.Manifest.EnvSecrets[ownerKeyEnv] != "" || observer.Manifest.Env[ownerKeyEnv] != "" {
		result.ManualChecks = append(result.ManualChecks, "The observer has a task owner key binding; remove it if no other process in that app needs it.")
	}
	return result, nil
}

func mcpTasksObserverAppUsable(app api.AppResponse) bool {
	return app.Status == "active" && app.DeploymentAvailability == api.AppDeploymentAvailabilityLive
}

func mcpTasksObserverEntrypointDetected(entrypoint []string) bool {
	for _, arg := range entrypoint {
		arg = strings.ToLower(arg)
		if strings.Contains(arg, "tasks-observer") || strings.Contains(arg, "tasks_observer") {
			return true
		}
	}
	return false
}

func mcpTasksObserverRoleConfigured(env map[string]string, entrypoint []string) bool {
	if strings.TrimSpace(env["MCP_TASKS_ROLE"]) != "observer" {
		return false
	}
	for _, arg := range entrypoint {
		if strings.TrimSpace(arg) != "" {
			return true
		}
	}
	return false
}

func mcpTasksObserverCredential(app api.AppResponse, env string, secrets api.AppSecretListResponse, metadataLoaded bool) mcpTasksObserverCredentialCheck {
	check := mcpTasksObserverCredentialCheck{Env: env, MetadataLoaded: metadataLoaded}
	ref := strings.TrimSpace(app.Manifest.EnvSecrets[env])
	if strings.HasPrefix(ref, api.SecretRefPrefix) {
		check.Configured = true
		check.SecretBacked = true
		key := strings.TrimPrefix(ref, api.SecretRefPrefix)
		check.SecretExists = metadataLoaded && mcpTasksObserverSecretListHasKey(secrets, key)
		check.Ready = check.SecretExists
		return check
	}
	check.Configured = strings.TrimSpace(app.Manifest.Env[env]) != ""
	check.Ready = false
	return check
}

func mcpTasksObserverSecretListHasKey(secrets api.AppSecretListResponse, key string) bool {
	for _, secret := range secrets.Secrets {
		if secret.Key == key {
			return true
		}
	}
	for _, scope := range secrets.SecretsByScope {
		for _, secret := range scope {
			if secret.Key == key {
				return true
			}
		}
	}
	return false
}

func mcpTasksObserverNamespacesMatch(worker, observer api.AppResponse) bool {
	workerNamespace, workerOK := worker.Manifest.Env[mcpTasksObserverNamespaceEnv]
	observerNamespace, observerOK := observer.Manifest.Env[mcpTasksObserverNamespaceEnv]
	if _, secret := worker.Manifest.EnvSecrets[mcpTasksObserverNamespaceEnv]; secret {
		workerOK = false
	}
	if _, secret := observer.Manifest.EnvSecrets[mcpTasksObserverNamespaceEnv]; secret {
		observerOK = false
	}
	return workerOK && observerOK && strings.TrimSpace(workerNamespace) != "" &&
		strings.TrimSpace(workerNamespace) == strings.TrimSpace(observerNamespace)
}

func mcpTasksObserverMetricsTargetMatches(app api.AppResponse, workerSlug string) bool {
	if _, secret := app.Manifest.EnvSecrets[mcpTasksObserverMetricsSlugEnv]; secret {
		return false
	}
	return strings.TrimSpace(app.Manifest.Env[mcpTasksObserverMetricsSlugEnv]) == workerSlug
}

func applyMCPTasksObserverMinimum(ctx context.Context, client *api.Client, observer api.AppResponse) (bool, error) {
	policy := api.ScalingPolicy{}
	if observer.ScalingPolicy != nil {
		policy = *observer.ScalingPolicy
	}
	defaultMax := policy.MaxInstances
	if defaultMax < 1 {
		defaultMax = 1
	}
	newScaling := api.WorkerScaling{Min: 1, Max: defaultMax}
	needsScalingUpdate := observer.Manifest.WorkerReplicas == nil
	if observer.Manifest.WorkerReplicas != nil {
		newScaling = *observer.Manifest.WorkerReplicas
		if newScaling.Min < 1 {
			newScaling.Min = 1
			needsScalingUpdate = true
		}
		if newScaling.Max < 1 {
			newScaling.Max = 1
			needsScalingUpdate = true
		}
	}
	if policy.MinInstances != newScaling.Min || policy.MaxInstances != newScaling.Max {
		needsScalingUpdate = true
	}
	if !needsScalingUpdate {
		return false, nil
	}
	policy.MinInstances = newScaling.Min
	policy.MaxInstances = newScaling.Max
	if _, err := client.UpdateApp(ctx, observer.Slug, api.UpdateAppRequest{
		WorkerReplicas: &newScaling,
		ScalingPolicy:  &policy,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func printMCPTasksObserverSetup(w io.Writer, result mcpTasksObserverSetupResult, applied bool) {
	_, _ = fmt.Fprintf(w, "MCP task observer status: %s → %s\n", result.ObserverSlug, result.WorkerSlug)
	_, _ = fmt.Fprintf(w, "  Observer app: ready=%t; minimum replicas configured=%t\n", result.ObserverApp.Ready, result.ObserverMinimumConfigured)
	tasksConfigStatus := "not checked"
	if result.TasksConfigLoaded {
		tasksConfigStatus = fmt.Sprintf("enabled=%t", result.TasksEnabled)
	}
	_, _ = fmt.Fprintf(w, "  Observer process: detected=%t; tasks config %s\n", result.ObserverProcessDetected, tasksConfigStatus)
	printMCPTasksObserverCredential(w, "Database", result.ObserverDatabaseCredential)
	printMCPTasksObserverCredential(w, "Worker database", result.WorkerDatabaseCredential)
	printMCPTasksObserverCredential(w, "Worker owner key", result.WorkerOwnerKeyCredential)
	printMCPTasksObserverCredential(w, "Observer metrics token", result.ObserverMetricsToken)
	printMCPTasksObserverCredential(w, "Worker metrics token", result.WorkerMetricsToken)
	_, _ = fmt.Fprintf(w, "  Metrics target: observer=%t worker=%t; namespace matches=%t\n", result.ObserverMetricsTargetMatches, result.WorkerMetricsTargetMatches, result.NamespaceMatches)
	_, _ = fmt.Fprintf(w, "  Observer heartbeat: fresh=%t; ready to link=%t\n", result.ObserverHeartbeatFresh, result.ReadyToLink)
	if applied || result.MinimumChanged {
		_, _ = fmt.Fprintln(w, "  Observer minimum: at least one replica is configured.")
	}
	if !result.ReadOnlyDatabaseGrantVerified {
		_, _ = fmt.Fprintln(w, "  Manual check: confirm the observer database role is read-only.")
	}
	if !result.MetricsTokenScopeVerified {
		_, _ = fmt.Fprintln(w, "  Manual check: restrict the metrics token to metrics:write.")
	}
	for _, manual := range result.ManualChecks[2:] {
		_, _ = fmt.Fprintf(w, "  Note: %s\n", manual)
	}
	for _, violation := range result.Violations {
		_, _ = fmt.Fprintf(w, "  Check: %s\n", violation)
	}
	if result.ReadyToLink {
		_, _ = fmt.Fprintf(w, "  Next: gregale mcp server tasks setup --app %s --observer-app %s --min 0 --apply\n", result.WorkerSlug, result.ObserverSlug)
	} else if !result.ObserverMinimumConfigured {
		_, _ = fmt.Fprintf(w, "  Next: gregale mcp server tasks observer setup --app %s --worker-app %s --config <gregale-mcp.json> --apply\n", result.ObserverSlug, result.WorkerSlug)
	} else {
		_, _ = fmt.Fprintf(w, "  Next: gregale mcp server tasks observer status --app %s --worker-app %s\n", result.ObserverSlug, result.WorkerSlug)
	}
}

func printMCPTasksObserverCredential(w io.Writer, label string, check mcpTasksObserverCredentialCheck) {
	state := "missing"
	switch {
	case check.Ready:
		state = "secret configured"
	case check.Configured && !check.SecretBacked:
		state = "configured without a secret reference"
	case check.SecretBacked && !check.MetadataLoaded:
		state = "secret reference present; existence could not be checked (requires secrets:read)"
	case check.SecretBacked:
		state = "secret reference missing from app secret store"
	}
	_, _ = fmt.Fprintf(w, "  %-24s %s (%s)\n", label+":", check.Env, state)
}
