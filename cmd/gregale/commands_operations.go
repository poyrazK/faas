package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func cmdOperations(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale operations <policy|bind-trigger|unbind-trigger|reconcile|start|start-job|get|wait|cancel>", "operations")
		return 1
	}
	switch args[0] {
	case "policy":
		return cmdOperationPolicy(args[1:])
	case "bind-trigger":
		return cmdOperationBindTrigger(args[1:])
	case "unbind-trigger":
		return cmdOperationUnbindTrigger(args[1:])
	case "reconcile":
		return cmdOperationReconcile(args[1:])
	case "start":
		return cmdOperationStart(args[1:])
	case "start-job":
		return cmdOperationStartJob(args[1:])
	case "get":
		return cmdOperationGet(args[1:])
	case "wait":
		return cmdOperationWait(args[1:])
	case "cancel":
		return cmdOperationCancel(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown operations command %q\n", args[0])
		return 1
	}
}

type exclusiveOperationsReconcileClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	ListJobs(context.Context, ...int) (api.ListJobsResponse, error)
	UpsertExclusiveWorkPolicy(context.Context, string, api.ExclusivePolicyRequest) (api.ExclusiveWorkPolicyRecord, error)
	ListCrons(context.Context, string) ([]api.CronResponse, error)
	ListInboundWebhookEndpoints(context.Context, string) ([]api.InboundWebhookEndpointResponse, error)
	GetTriggers(context.Context, string, api.TriggerKind) ([]api.Trigger, error)
	UpsertExclusiveTriggerBinding(context.Context, string, string, api.ExclusiveTriggerBindingRequest) (api.ExclusiveTriggerBindingRecord, error)
}

type exclusiveOperationsReconcileReport struct {
	Policies int `json:"policies"`
	Bindings int `json:"bindings"`
}

type resolvedExclusiveManifestBinding struct {
	source  string
	trigger string
	request api.ExclusiveTriggerBindingRequest
}

func reconcileExclusiveOperations(ctx context.Context, client exclusiveOperationsReconcileClient, dir string) (exclusiveOperationsReconcileReport, error) {
	var report exclusiveOperationsReconcileReport
	manifest, ok, err := gregalemanifest.Load(dir)
	if err != nil {
		return report, err
	}
	if !ok || manifest == nil || manifest.ExclusiveOperations == nil {
		return report, fmt.Errorf("no exclusive_operations section found in %s", dir)
	}
	config := manifest.ExclusiveOperations
	if err := config.Validate(); err != nil {
		return report, err
	}

	apps := make(map[string]api.AppResponse)
	resolveApp := func(slug string) (api.AppResponse, error) {
		if app, found := apps[slug]; found {
			return app, nil
		}
		app, err := client.GetApp(ctx, slug)
		if err != nil {
			return api.AppResponse{}, fmt.Errorf("resolve operation member app %q: %w", slug, err)
		}
		apps[slug] = app
		return app, nil
	}
	for _, policy := range config.Policies {
		for _, slug := range policy.MemberApps {
			if _, err := resolveApp(slug); err != nil {
				return report, err
			}
		}
	}

	jobs := make(map[string]api.JobResponse)
	if hasExclusiveJobMembers(config) {
		for offset := 0; ; {
			page, listErr := client.ListJobs(ctx, 200, offset)
			if listErr != nil {
				return report, fmt.Errorf("list Jobs for exclusive policy members: %w", listErr)
			}
			for _, job := range page.Jobs {
				jobs[job.ID] = job
			}
			if page.NextOffset <= offset || page.NextOffset >= page.Total {
				break
			}
			offset = page.NextOffset
		}
		for _, policy := range config.Policies {
			for _, id := range policy.MemberJobIDs {
				if _, found := jobs[id]; !found {
					return report, fmt.Errorf("resolve operation member Job %q: no Job with that ID is available in the authenticated account", id)
				}
			}
		}
	}

	// Resolve every resource before writing any account policy or binding.
	// This makes typos and stale selectors fail without a partial update.
	resolved := make([]resolvedExclusiveManifestBinding, 0, len(config.Bindings))
	for _, binding := range config.Bindings {
		var app api.AppResponse
		if binding.Source != "job_schedule" {
			var err error
			app, err = resolveApp(binding.App)
			if err != nil {
				return report, err
			}
		}
		key, err := binding.KeyJSON()
		if err != nil {
			return report, fmt.Errorf("exclusive operation binding for target %q: %w", binding.App+binding.Job, err)
		}
		request := api.ExclusiveTriggerBindingRequest{
			Policy: binding.Policy, Key: key, PlatformTenantID: binding.PlatformTenantID,
			EquivalenceKey: binding.EquivalenceKey,
		}
		var id string
		switch binding.Source {
		case "job_schedule":
			matches := make([]api.JobResponse, 0, 1)
			for _, job := range jobs {
				if job.Name == binding.Job {
					matches = append(matches, job)
				}
			}
			if len(matches) != 1 {
				return report, fmt.Errorf("Job schedule selector name=%q matched %d resources; expected one", binding.Job, len(matches))
			}
			job := matches[0]
			if job.Kind != "recurring" || job.Schedule == "" {
				return report, fmt.Errorf("Job schedule selector name=%q does not identify a recurring Job", binding.Job)
			}
			member := false
			for _, declaration := range config.Policies {
				if declaration.Name == binding.Policy {
					member = slices.Contains(declaration.MemberJobIDs, job.ID)
					break
				}
			}
			if !member {
				return report, fmt.Errorf("Job %q is not a member of policy %q", binding.Job, binding.Policy)
			}
			id = job.ID
		case "cron":
			crons, listErr := client.ListCrons(ctx, binding.App)
			if listErr != nil {
				return report, fmt.Errorf("list crons for app %q: %w", binding.App, listErr)
			}
			matches := make([]api.CronResponse, 0, 1)
			for _, cron := range crons {
				if cron.Kind != "command" && cron.Schedule == binding.Schedule && cron.Path == binding.Path {
					matches = append(matches, cron)
				}
			}
			if len(matches) != 1 {
				return report, fmt.Errorf("cron selector app=%q schedule=%q path=%q matched %d resources; expected one", binding.App, binding.Schedule, binding.Path, len(matches))
			}
			id = matches[0].ID
		case "inbound_webhook":
			endpoints, listErr := client.ListInboundWebhookEndpoints(ctx, binding.App)
			if listErr != nil {
				return report, fmt.Errorf("list inbound webhooks for app %q: %w", binding.App, listErr)
			}
			matches := make([]api.InboundWebhookEndpointResponse, 0, 1)
			for _, endpoint := range endpoints {
				if endpoint.Name == binding.Name {
					matches = append(matches, endpoint)
				}
			}
			if len(matches) != 1 {
				return report, fmt.Errorf("inbound webhook selector app=%q name=%q matched %d resources; expected one", binding.App, binding.Name, len(matches))
			}
			id = matches[0].ID
		case "broker":
			triggers, listErr := client.GetTriggers(ctx, app.ID, api.TriggerKind(binding.Kind))
			if listErr != nil {
				return report, fmt.Errorf("list %s triggers for app %q: %w", binding.Kind, binding.App, listErr)
			}
			matches := make([]api.Trigger, 0, 1)
			for _, trigger := range triggers {
				if trigger.Slug == binding.Name {
					matches = append(matches, trigger)
				}
			}
			if len(matches) != 1 {
				return report, fmt.Errorf("broker selector app=%q kind=%q name=%q matched %d resources; expected one", binding.App, binding.Kind, binding.Name, len(matches))
			}
			id = matches[0].ID
		}
		resolved = append(resolved, resolvedExclusiveManifestBinding{source: binding.Source, trigger: id, request: request})
	}

	for _, declaration := range config.Policies {
		memberIDs := make([]string, 0, len(declaration.MemberApps))
		for _, slug := range declaration.MemberApps {
			memberIDs = append(memberIDs, apps[slug].ID)
		}
		policy := api.ExclusivePolicyRequest{
			Name: declaration.Name, Scope: declaration.Scope, EnvironmentID: declaration.EnvironmentID,
			MemberAppIDs: memberIDs, MemberJobIDs: append([]string(nil), declaration.MemberJobIDs...), Contention: declaration.Contention,
			LeaseSeconds: declaration.LeaseSeconds, MaxAttemptSeconds: declaration.MaxAttemptSeconds,
			MaxAttempts: declaration.MaxAttempts, RetryAfterSeconds: declaration.RetryAfterSeconds,
		}
		if _, err := client.UpsertExclusiveWorkPolicy(ctx, declaration.Name, policy); err != nil {
			return report, fmt.Errorf("apply exclusive operation policy %q: %w", declaration.Name, err)
		}
		report.Policies++
	}
	for _, binding := range resolved {
		if _, err := client.UpsertExclusiveTriggerBinding(ctx, binding.source, binding.trigger, binding.request); err != nil {
			return report, fmt.Errorf("bind %s trigger %s: %w", binding.source, binding.trigger, err)
		}
		report.Bindings++
	}
	return report, nil
}

func hasExclusiveJobMembers(config *gregalemanifest.ExclusiveOperationsConfig) bool {
	if config == nil {
		return false
	}
	for _, policy := range config.Policies {
		if len(policy.MemberJobIDs) > 0 {
			return true
		}
	}
	return false
}

func cmdOperationReconcile(args []string) int {
	fs := newFlagSet("operations reconcile", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory containing gregale.yaml or gregale.toml")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale operations reconcile [--dir PROJECT_DIR]", "operations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	report, err := reconcileExclusiveOperations(context.Background(), client, *dir)
	if err != nil {
		return printErr("Could not reconcile managed operation configuration", err)
	}
	if jsonOutput {
		return writeJSONStdout(report)
	}
	fmt.Fprintf(os.Stdout, "Reconciled %d managed operation policy/policies and %d trigger binding(s).\n", report.Policies, report.Bindings)
	return 0
}

func cmdOperationBindTrigger(args []string) int {
	fs := newFlagSet("operations bind-trigger", flag.ContinueOnError)
	policy := fs.String("policy", "", "managed operation policy")
	key := fs.String("key", "", "JSON scalar business coordination key")
	tenant := fs.String("tenant", "", "account-authorized platform tenant ID for a tenant-scoped policy")
	equivalence := fs.String("equivalence-key", "", "equivalent request identity for join_existing policies")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || *policy == "" || *key == "" || !json.Valid([]byte(*key)) {
		PrintUsage(os.Stderr, "usage: gregale operations bind-trigger --policy NAME --key JSON [--tenant ID] [--equivalence-key KEY] <cron|inbound_webhook|broker> <trigger-id>", "operations")
		return 1
	}
	var scalar any
	if err := json.Unmarshal([]byte(*key), &scalar); err != nil {
		return 1
	}
	switch scalar.(type) {
	case string, float64, bool:
	default:
		fmt.Fprintln(osStderr, "operation key must be a JSON string, number, or boolean")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	row, err := client.UpsertExclusiveTriggerBinding(context.Background(), fs.Arg(0), fs.Arg(1), api.ExclusiveTriggerBindingRequest{
		Policy: *policy, Key: json.RawMessage(*key), PlatformTenantID: *tenant, EquivalenceKey: *equivalence,
	})
	if err != nil {
		return printErr("Could not bind trigger to managed operation", err)
	}
	if jsonOutput {
		return writeJSONStdout(row)
	}
	fmt.Fprintf(os.Stdout, "Bound %s %s to operation policy %s (app %s).\n", row.Source, row.TriggerID, row.Policy, row.AppID)
	return 0
}

func cmdOperationUnbindTrigger(args []string) int {
	fs := newFlagSet("operations unbind-trigger", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		PrintUsage(os.Stderr, "usage: gregale operations unbind-trigger <cron|inbound_webhook|broker> <trigger-id>", "operations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteExclusiveTriggerBinding(context.Background(), fs.Arg(0), fs.Arg(1)); err != nil {
		return printErr("Could not remove trigger operation binding", err)
	}
	if !jsonOutput {
		fmt.Fprintln(os.Stdout, "Removed managed-operation trigger binding.")
	}
	return 0
}

func cmdOperationPolicy(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale operations policy <list|upsert>", "operations")
		return 1
	}
	switch args[0] {
	case "list":
		fs := newFlagSet("operations policy list", flag.ContinueOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if fs.NArg() != 0 {
			return 1
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		rows, err := client.ListExclusiveWorkPolicies(context.Background())
		if err != nil {
			return printErr("Could not list operation policies", err)
		}
		if jsonOutput {
			return writeJSONStdout(rows)
		}
		for _, row := range rows.Policies {
			fmt.Fprintf(os.Stdout, "%s (revision %d): scope=%s contention=%s lease=%ds members=%d retired=%t\n", row.Policy.Name, row.Revision, row.Policy.Scope, row.Policy.Contention, row.Policy.LeaseSeconds, len(row.Policy.MemberAppIDs)+len(row.Policy.MemberJobIDs), row.Retired)
		}
		return 0
	case "upsert":
		fs := newFlagSet("operations policy upsert", flag.ContinueOnError)
		policyFile := fs.String("file", "", "JSON file containing the policy")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if fs.NArg() != 1 || *policyFile == "" {
			PrintUsage(os.Stderr, "usage: gregale operations policy upsert --file POLICY.json <name>", "operations")
			return 1
		}
		data, err := os.ReadFile(*policyFile)
		if err != nil {
			return printErr("Could not read operation policy", err)
		}
		var policy api.ExclusivePolicyRequest
		if err = json.Unmarshal(data, &policy); err != nil {
			return printErr("Invalid operation policy JSON", err)
		}
		if policy.Name != "" && policy.Name != fs.Arg(0) {
			fmt.Fprintln(osStderr, "policy name must match the positional name")
			return 1
		}
		policy.Name = fs.Arg(0)
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		row, err := client.UpsertExclusiveWorkPolicy(context.Background(), fs.Arg(0), policy)
		if err != nil {
			return printErr("Could not save operation policy", err)
		}
		if jsonOutput {
			return writeJSONStdout(row)
		}
		fmt.Fprintf(os.Stdout, "Saved operation policy %s (revision %d).\n", row.Policy.Name, row.Revision)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown operation policy command %q\n", args[0])
		return 1
	}
}

func cmdOperationStart(args []string) int {
	fs := newFlagSet("operations start", flag.ContinueOnError)
	policy := fs.String("policy", "", "managed operation policy")
	key := fs.String("key", "", "JSON scalar concurrency key")
	requestKey := fs.String("idempotency-key", "", "stable request retry key")
	equivalence := fs.String("equivalence-key", "", "equivalent request identity for join_existing policies")
	tenant := fs.String("tenant", "", "account-authorized customer tenant ID")
	self := fs.Bool("self", false, "derive customer tenant scope from a platform-tenant credential")
	payload := fs.String("payload", "{}", "JSON request body")
	method := fs.String("method", http.MethodPost, "HTTP method delivered to the app")
	path := fs.String("path", "/", "app route")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *policy == "" || *key == "" || !json.Valid([]byte(*key)) || !json.Valid([]byte(*payload)) || (*self && *tenant != "") {
		PrintUsage(os.Stderr, "usage: gregale operations start --policy NAME --key JSON [--tenant ID|--self] [--equivalence-key KEY] [--idempotency-key KEY] [--payload JSON] <app-slug>", "operations")
		return 1
	}
	var scalar any
	if err := json.Unmarshal([]byte(*key), &scalar); err != nil {
		fmt.Fprintln(osStderr, "operation key must be a JSON string, number, or boolean")
		return 1
	}
	if scalar == nil {
		fmt.Fprintln(osStderr, "operation key must be a JSON string, number, or boolean")
		return 1
	}
	if _, ok := scalar.(map[string]any); ok {
		fmt.Fprintln(osStderr, "operation key must be a JSON scalar")
		return 1
	}
	if _, ok := scalar.([]any); ok {
		fmt.Fprintln(osStderr, "operation key must be a JSON scalar")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	req := api.ExclusiveOperationRequest{Policy: *policy, Key: json.RawMessage(*key), EquivalenceKey: *equivalence, Invocation: api.InvokeRequest{Method: *method, Path: *path, Payload: json.RawMessage(*payload)}}
	var accepted api.ExclusiveOperationAccepted
	if *self {
		accepted, err = client.SubmitPlatformTenantExclusiveOperation(context.Background(), fs.Arg(0), req, *requestKey)
	} else {
		accepted, err = client.SubmitExclusiveOperation(context.Background(), fs.Arg(0), *tenant, req, *requestKey)
	}
	if err != nil {
		return printErr("Could not start managed operation", err)
	}
	if jsonOutput {
		return writeJSONStdout(accepted)
	}
	fmt.Fprintf(os.Stdout, "Operation %s accepted (%s).\n", accepted.ID, accepted.StatusURL)
	if accepted.Joined {
		fmt.Fprintln(os.Stdout, "Joined an equivalent active operation.")
	}
	return 0
}

func cmdOperationStartJob(args []string) int {
	fs := newFlagSet("operations start-job", flag.ContinueOnError)
	policy := fs.String("policy", "", "managed operation policy")
	key := fs.String("key", "", "JSON scalar business coordination key")
	equivalence := fs.String("equivalence-key", "", "equivalent request identity for join_existing policies")
	requestKey := fs.String("idempotency-key", "", "stable request retry key")
	tasks := fs.Int("tasks", 1, "number of Job tasks when --run-file is omitted")
	runFile := fs.String("run-file", "", "JSON CreateJobRunRequest to submit")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *policy == "" || *key == "" || !json.Valid([]byte(*key)) || *tasks < 1 || *tasks > 5000 {
		PrintUsage(os.Stderr, "usage: gregale operations start-job --policy NAME --key JSON [--equivalence-key KEY] [--idempotency-key KEY] [--tasks N | --run-file FILE] <job-name>", "operations")
		return 1
	}
	var scalar any
	if err := json.Unmarshal([]byte(*key), &scalar); err != nil || scalar == nil {
		fmt.Fprintln(osStderr, "operation key must be a JSON string, number, or boolean")
		return 1
	}
	switch scalar.(type) {
	case map[string]any, []any:
		fmt.Fprintln(osStderr, "operation key must be a JSON scalar")
		return 1
	}
	run := api.CreateJobRunRequest{Tasks: *tasks}
	if *runFile != "" {
		tasksProvided := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "tasks" {
				tasksProvided = true
			}
		})
		if tasksProvided {
			fmt.Fprintln(osStderr, "--tasks cannot be combined with --run-file")
			return 1
		}
		body, err := os.ReadFile(*runFile)
		if err != nil {
			return printErr("Could not read Job run request", err)
		}
		if !json.Valid(body) || json.Unmarshal(body, &run) != nil {
			fmt.Fprintln(osStderr, "--run-file must contain a valid CreateJobRunRequest JSON object")
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	accepted, err := client.SubmitExclusiveJobOperation(context.Background(), fs.Arg(0), api.ExclusiveJobOperationRequest{
		Policy: *policy, Key: json.RawMessage(*key), EquivalenceKey: *equivalence, Run: run,
	}, *requestKey)
	if err != nil {
		return printErr("Could not start managed Job operation", err)
	}
	if jsonOutput {
		return writeJSONStdout(accepted)
	}
	fmt.Fprintf(os.Stdout, "Operation %s accepted (%s).\n", accepted.ID, accepted.StatusURL)
	if accepted.Joined {
		fmt.Fprintln(os.Stdout, "Joined an equivalent active operation.")
	}
	return 0
}

func cmdOperationGet(args []string) int {
	fs := newFlagSet("operations get", flag.ContinueOnError)
	self := fs.Bool("self", false, "use the authenticated platform-customer scope")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale operations get [--self] <id>", "operations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var row api.ExclusiveOperationRecord
	if *self {
		row, err = client.GetPlatformTenantExclusiveOperation(context.Background(), fs.Arg(0))
	} else {
		row, err = client.GetExclusiveOperation(context.Background(), fs.Arg(0))
	}
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			renderAPIError(os.Stderr, ae)
			return exitCodeForStatus(ae.Problem.Status)
		}
		return printErr("Could not inspect managed operation", err)
	}
	if jsonOutput {
		return writeJSONStdout(row)
	}
	target := "app " + row.AppID
	if row.JobID != "" {
		target = "Job " + row.JobID
	}
	fmt.Fprintf(os.Stdout, "ID: %s\nState: %s\nGeneration: %d\nTarget: %s\n", row.ID, row.State, row.Generation, target)
	if row.LastError != "" {
		fmt.Fprintf(os.Stdout, "Last error: %s\n", row.LastError)
	}
	if len(row.Result) > 0 {
		fmt.Fprintf(os.Stdout, "Result: %s\n", row.Result)
	}
	return 0
}

func cmdOperationWait(args []string) int {
	fs := newFlagSet("operations wait", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 0, "stop waiting after this duration")
	interval := fs.Duration("interval", time.Second, "poll interval")
	self := fs.Bool("self", false, "use the authenticated platform-customer scope")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *timeout < 0 || *interval <= 0 {
		PrintUsage(os.Stderr, "usage: gregale operations wait [--timeout D] [--interval D] <id>", "operations")
		return 1
	}
	ctx := context.Background()
	cancel := func() {}
	if *timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, *timeout)
	}
	defer cancel()
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	for {
		var row api.ExclusiveOperationRecord
		var getErr error
		if *self {
			row, getErr = client.GetPlatformTenantExclusiveOperation(ctx, fs.Arg(0))
		} else {
			row, getErr = client.GetExclusiveOperation(ctx, fs.Arg(0))
		}
		if getErr != nil {
			return printErr("Could not inspect managed operation", getErr)
		}
		if row.State != "pending" && row.State != "running" {
			if jsonOutput {
				return writeJSONStdout(row)
			}
			fmt.Fprintf(os.Stdout, "Operation %s: %s\n", row.ID, row.State)
			if row.State == "completed" {
				return 0
			}
			return 1
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return printErr("Stopped waiting; operation may still be active", ctx.Err())
		case <-timer.C:
		}
	}
}

func cmdOperationCancel(args []string) int {
	fs := newFlagSet("operations cancel", flag.ContinueOnError)
	self := fs.Bool("self", false, "use the authenticated platform-customer scope")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale operations cancel [--self] <id>", "operations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *self {
		err = client.CancelPlatformTenantExclusiveOperation(context.Background(), fs.Arg(0))
	} else {
		err = client.CancelExclusiveOperation(context.Background(), fs.Arg(0))
	}
	if err != nil {
		return printErr("Could not cancel managed operation", err)
	}
	if !jsonOutput {
		fmt.Fprintln(os.Stdout, "Cancellation requested.")
	}
	return 0
}

func writeJSONStdout(value any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		fmt.Fprintln(osStderr, err)
		return 1
	}
	return 0
}
