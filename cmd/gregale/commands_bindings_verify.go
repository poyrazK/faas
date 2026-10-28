package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	bindingProbeTaskTimeoutSeconds = 15
	bindingProbeWaitTimeoutDefault = 5 * time.Minute
	bindingProbeCancelTimeout      = 5 * time.Second
)

type serviceBindingProbeClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	CreateAppTask(context.Context, string, api.CreateAppTaskRequest) (api.AppTaskResponse, error)
	GetAppTask(context.Context, string, string) (api.AppTaskResponse, error)
	CancelAppTask(context.Context, string, string) (api.AppTaskResponse, error)
}

type bindingInventoryProbeClient interface {
	serviceBindingProbeClient
	GetAppBindingInventory(context.Context, string, string) (api.AppBindingInventory, error)
	ListOutboundAppBindings(context.Context, string) (api.OutboundAppBindingList, error)
}

type serviceBindingProbeBatchItem struct {
	QueueReport         *api.QueueBindingCanary              `json:"queue_report,omitempty"`
	Error               string                               `json:"error,omitempty"`
	Service             string                               `json:"service,omitempty"`
	Type                string                               `json:"type"`
	Name                string                               `json:"name,omitempty"`
	Binding             string                               `json:"binding"`
	Scope               string                               `json:"scope"`
	PostgresReport      *api.PostgresBindingProbeReport      `json:"postgres_report,omitempty"`
	OutboundReport      *api.OutboundBindingProbeReport      `json:"outbound_report,omitempty"`
	ObjectStorageReport *api.ObjectStorageBindingProbeReport `json:"object_storage_report,omitempty"`
	Status              string                               `json:"status"`
	Reason              string                               `json:"reason,omitempty"`
	Report              *api.ServiceBindingProbeReport       `json:"report,omitempty"`
}

type serviceBindingProbeBatchReport struct {
	Gate         *allBindingCoverageGate        `json:"gate,omitempty"`
	DeploymentID string                         `json:"deployment_id,omitempty"`
	App          string                         `json:"app"`
	Scope        string                         `json:"scope"`
	Issues       []api.BindingInventoryIssue    `json:"issues,omitempty"`
	Coverage     string                         `json:"coverage"`
	Unsupported  int                            `json:"unsupported"`
	NotChecked   int                            `json:"not_checked"`
	Total        int                            `json:"total"`
	Checked      int                            `json:"checked"`
	Passed       int                            `json:"passed"`
	Failed       int                            `json:"failed"`
	Skipped      int                            `json:"skipped"`
	Bindings     []serviceBindingProbeBatchItem `json:"bindings"`
}

func cmdBindingsVerify(args []string) int {
	fs := newFlagSet("bindings-verify", flag.ContinueOnError)
	durable := fs.Bool("durable", false, "persist a fixed-deadline single-app verification run")
	runID := fs.String("run-id", "", "caller-selected run UUID for durable create recovery")
	progressMode := fs.String("progress", "auto", "durable progress on stderr: auto, plain, ndjson or off")
	statusRetries := fs.Int("status-read-retries", api.BindingVerificationRunnerDefaultStatusReadRetries, "transient durable status GET retries (0–8)")
	concurrency := fs.Int("concurrency", bindingVerificationConcurrencyDefault, "maximum concurrent release-policy probes (1–8)")
	all := fs.Bool("all", false, "verify services, PostgreSQL, storage, outbound and eligible queues (publication opt-in)")
	requireComplete := fs.Bool("require-complete", false, "fail --all unless every eligible binding is checked and passes")
	queue := fs.String("queue", "", "verify one push queue binding with a safe message")
	queuePublish := fs.Bool("queue-publish", false, "consent to publishing a bounded safe queue canary")
	releasePolicy := fs.Bool("release-policy", false, "reuse current evidence, verify required bindings and wait for policy-aware preflight")
	dryRun := fs.Bool("dry-run", false, "preview --all or --release-policy probes and required consent without creating tasks or messages")
	postgresKey := fs.String("postgres", "", "verify a managed PostgreSQL binding by environment key")
	objectStoragePrefix := fs.String("object-storage", "", "verify an object-storage binding by environment prefix (read access only)")
	objectStorageWrite := fs.Bool("object-storage-write", false, "opt into storage upload and cleanup (plus download for read/write); --all requires --deployment")
	outboundID := fs.String("outbound", "", "verify a configured outbound integration by UUID")
	deployment := fs.String("deployment", "", "exact live deployment id or vN revision to verify, including zero-traffic candidates")
	pollInterval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval while the canary runs")
	waitTimeout := fs.Duration("wait-timeout", bindingProbeWaitTimeoutDefault, "maximum time for the CLI to wait for canary task(s)")
	flagArgs, positionals := splitArgsForFlags(args, "all", "require-complete", "object-storage-write", "release-policy", "dry-run", "queue-publish", "durable")
	if err := fs.Parse(flagArgs); err != nil || fs.NArg() != 0 {
		printBindingsVerifyUsage()
		return 1
	}
	explicitProgress := false
	fs.Visit(func(f *flag.Flag) { explicitProgress = explicitProgress || f.Name == "progress" })
	if !validBindingProgress(*progressMode) || explicitProgress && (!*durable || *dryRun) {
		return printErr("Invalid progress option", errors.New("--progress auto|plain|ndjson|off requires durable execution"))
	}
	explicitConcurrency := false
	fs.Visit(func(f *flag.Flag) { explicitConcurrency = explicitConcurrency || f.Name == "concurrency" })
	explicitRetries := false
	fs.Visit(func(f *flag.Flag) { explicitRetries = explicitRetries || f.Name == "status-read-retries" })
	if *statusRetries < 0 || *statusRetries > api.BindingVerificationRunnerMaxStatusReadRetries || explicitRetries && !*durable {
		return printErr("Invalid status read retries", errors.New("--status-read-retries (0–8) requires --durable"))
	}
	if *concurrency < 1 || *concurrency > bindingVerificationConcurrencyMax || explicitConcurrency && !*releasePolicy {
		printBindingsVerifyUsage()
		return 1
	}
	if *durable && (!*releasePolicy || *waitTimeout < time.Second) || *runID != "" && !*durable {
		printBindingsVerifyUsage()
		return 1
	}
	if *runID != "" {
		id, err := uuid.Parse(*runID)
		if err != nil || id == uuid.Nil {
			printBindingsVerifyUsage()
			return 1
		}
		*runID = id.String()
	}
	postgresKeyValue := strings.TrimSpace(*postgresKey)
	objectStoragePrefixValue := strings.TrimSpace(*objectStoragePrefix)
	outboundValue := strings.TrimSpace(*outboundID)
	selections := 0
	for _, selected := range []bool{*all, *releasePolicy, postgresKeyValue != "", objectStoragePrefixValue != "", outboundValue != "", *queue != ""} {
		if selected {
			selections++
		}
	}
	if outboundValue != "" {
		id, err := uuid.Parse(outboundValue)
		if err != nil {
			printBindingsVerifyUsage()
			return 1
		}
		outboundValue = id.String()
	}
	invalidSelection := *requireComplete && !*all || *queuePublish && *queue == "" && !*releasePolicy && !*all || *queuePublish && *all && *deployment == "" || *queue != "" && (!api.ValidQueueBindingName(*queue) || *deployment == "") || selections > 1 || *objectStorageWrite && objectStoragePrefixValue == "" && !*releasePolicy && !*all || *objectStorageWrite && *all && *deployment == "" || *dryRun && !*releasePolicy && !*all
	if selections > 0 {
		invalidSelection = invalidSelection || len(positionals) != 1
	} else {
		invalidSelection = invalidSelection || len(positionals) != 2
	}
	if invalidSelection {
		printBindingsVerifyUsage()
		return 1
	}
	slug := strings.TrimSpace(positionals[0])
	invalidPostgresKey := postgresKeyValue != "" && api.ValidateEnvKey(postgresKeyValue) != nil
	invalidObjectStoragePrefix := objectStoragePrefixValue != "" && !api.ValidObjectStorageBindingPrefix(objectStoragePrefixValue)
	if !api.ValidAppSlug(slug) || *pollInterval <= 0 || *waitTimeout <= 0 || invalidPostgresKey || invalidObjectStoragePrefix || !validBindingDeploymentFlag(*deployment) {
		printBindingsVerifyUsage()
		return 1
	}
	service := ""
	if selections == 0 {
		services, normalizeErr := api.NormalizeServiceBindingTargets([]string{strings.TrimSpace(positionals[1])})
		if normalizeErr != nil || len(services) != 1 {
			printBindingsVerifyUsage()
			return 1
		}
		service = services[0]
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *durable {
		return runDurableBindingVerification(withBindingProgress(withBindingStatusReadRetries(withBindingVerificationBudget(context.Background(), *concurrency), statusRetries), *progressMode), client, slug, *deployment, *runID, *dryRun, *objectStorageWrite, *queuePublish, *pollInterval, *waitTimeout)
	}
	if *releasePolicy {
		return runBindingPolicyVerification(withBindingVerificationBudget(context.WithValue(context.Background(), queueCanaryConsentKey{}, *queuePublish), *concurrency), client, slug, *deployment, *dryRun, *objectStorageWrite, *pollInterval, *waitTimeout)
	}
	if *queue != "" {
		return runQueueBindingCanary(context.Background(), client, slug, *queue, *deployment, *queuePublish, *pollInterval, *waitTimeout)
	}
	probeClient, err := selectBindingProbeClient(context.Background(), client, slug, *deployment)
	if err != nil {
		return printErr("Could not select binding verification deployment", err)
	}
	if *all {
		return runAllBindingProbes(context.Background(), probeClient, slug, *pollInterval, *waitTimeout, allBindingProbeOptions{client: client, publish: *queuePublish, requireComplete: *requireComplete, objectStorageWrite: *objectStorageWrite, dryRun: *dryRun})
	}
	if postgresKeyValue != "" {
		return runPostgresBindingProbe(context.Background(), probeClient, slug, postgresKeyValue, *pollInterval, *waitTimeout)
	}
	if outboundValue != "" {
		return runOutboundBindingProbe(context.Background(), probeClient, slug, outboundValue, *pollInterval, *waitTimeout)
	}
	if objectStoragePrefixValue != "" {
		return runObjectStorageBindingProbeWithWrite(context.Background(), probeClient, slug, objectStoragePrefixValue, *objectStorageWrite, *pollInterval, *waitTimeout)
	}
	return runServiceBindingProbe(context.Background(), probeClient, slug, service, *pollInterval, *waitTimeout)
}

func printBindingsVerifyUsage() {
	PrintUsage(osStderr, "usage: gregale app binding verify <app> <service> [--deployment ID|vN] [flags] | gregale app binding verify <app> --all [--dry-run] [--require-complete] [--deployment ID|vN] [--queue-publish] [--object-storage-write] [flags] | gregale app binding verify <app> --release-policy [--dry-run] [--object-storage-write] [flags] | gregale app binding verify <app> --postgres <ENVIRONMENT_KEY> [flags] | gregale app binding verify <app> --object-storage <PREFIX> [flags] | gregale app binding verify <app> --outbound <INTEGRATION_ID> [flags]", "bindings")
}

func runPostgresBindingProbe(ctx context.Context, client bindingInventoryProbeClient, slug, environmentKey string, pollInterval, waitTimeout time.Duration) int {
	inventory, err := client.GetAppBindingInventory(ctx, slug, "")
	if err != nil {
		return printErr("Could not load app bindings", err)
	}
	bound := false
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypePostgres && item.Binding == environmentKey && item.Scope == inventory.VerificationScope {
			if item.Access == "migration" {
				return printErr("Migration bindings are restricted to release tasks", fmt.Errorf("verify this connection through the deployment release command"))
			}
			bound = true
			break
		}
	}
	if !bound {
		return printErr("PostgreSQL key is not bound in the selected live scope", fmt.Errorf("%s has no selected managed PostgreSQL binding for %s", slug, environmentKey))
	}

	report, task, errorTitle, exitCode, err := executePostgresBindingProbe(ctx, client, slug, environmentKey, pollInterval, waitTimeout)
	if errorTitle != "" {
		return printErr(errorTitle, err)
	}
	if exitCode == 130 {
		return exitCode
	}
	if exitCode == 0 && bindingProbeDeploymentChanged(inventory, task) {
		report.Error = "live deployment changed during verification; run verification again"
		exitCode = 1
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return jsonOut(err)
		}
		return exitCode
	}
	renderPostgresBindingProbeReport(slug, report, task)
	return exitCode
}

func executePostgresBindingProbe(ctx context.Context, client serviceBindingProbeClient, slug, environmentKey string, pollInterval, waitTimeout time.Duration) (api.PostgresBindingProbeReport, api.AppTaskResponse, string, int, error) {
	report := newPostgresBindingProbeReport(slug, environmentKey)
	task, err := client.CreateAppTask(ctx, slug, api.CreateAppTaskRequest{
		Command:        []string{api.AppTaskPostgresBindingProbeCommand, environmentKey},
		TimeoutSeconds: bindingProbeTaskTimeoutSeconds,
		MaxOutputBytes: 4096,
	})
	if err != nil {
		report.Error = err.Error()
		return report, task, "Could not start PostgreSQL binding canary", 1, err
	}
	report.TaskID = task.ID
	report.DeploymentID = task.DeploymentID
	interruptContext, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	waitContext, cancel := context.WithTimeout(interruptContext, waitTimeout)
	defer cancel()
	for !task.Status.Terminal() {
		select {
		case <-waitContext.Done():
			if bindingProbeAutoCancel(ctx) && errors.Is(interruptContext.Err(), context.Canceled) {
				cancelContext, cancelRequest := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
				defer cancelRequest()
				cancelled, cancelErr := client.CancelAppTask(cancelContext, slug, task.ID)
				if cancelErr != nil {
					PrintWarn(osStderr, "could not request cancellation for PostgreSQL canary task %s: %v", task.ID, cancelErr)
				} else if !jsonOutput {
					PrintWarn(osStderr, "cancellation requested for PostgreSQL canary task %s (status=%s)", task.ID, cancelled.Status)
				}
				return report, task, "", 130, nil
			}
			report.Error = "PostgreSQL canary task did not finish before the wait timeout"
			return report, task, "PostgreSQL binding canary is still running", 1, waitContext.Err()
		case <-time.After(pollInterval):
		}
		task, err = client.GetAppTask(waitContext, slug, task.ID)
		if err != nil {
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
				report.Error = "PostgreSQL canary task did not finish before the wait timeout"
				return report, task, "PostgreSQL binding canary wait timed out; the task is still running", 1, waitContext.Err()
			}
			report.Error = err.Error()
			return report, task, "Could not read PostgreSQL binding canary status", 1, err
		}
	}
	if task.StdoutTail != "" {
		var taskReport api.PostgresBindingProbeReport
		if err := json.Unmarshal([]byte(task.StdoutTail), &taskReport); err != nil {
			report.Error = "task did not return a valid PostgreSQL canary report"
		} else {
			report = taskReport
			if report.EnvironmentKey != environmentKey {
				report.Error = "canary report does not match the selected PostgreSQL key"
			}
		}
	} else {
		report.Error = "task did not return a PostgreSQL canary report"
	}
	if report.EnvironmentKey == "" {
		report.EnvironmentKey = environmentKey
	}
	report.App = slug
	report.TaskID = task.ID
	report.DeploymentID = task.DeploymentID
	if task.OutputTruncated {
		report.Error = "canary output was truncated"
	}
	if task.Status != api.AppTaskStatusSucceeded || !report.Passed() || task.OutputTruncated || report.Error != "" || (task.ExitCode != nil && *task.ExitCode != 0) {
		if report.Error == "" {
			report.Error = "PostgreSQL binding canary did not succeed"
		}
		return report, task, "", 1, nil
	}
	return report, task, "", 0, nil
}

func newPostgresBindingProbeReport(app, environmentKey string) api.PostgresBindingProbeReport {
	return api.PostgresBindingProbeReport{
		App:            app,
		EnvironmentKey: environmentKey,
		Environment:    api.PostgresBindingProbeCheck{Status: "not_checked"},
		Configuration:  api.PostgresBindingProbeCheck{Status: "not_checked"},
		Connection:     api.PostgresBindingProbeCheck{Status: "not_checked"},
		Query:          api.PostgresBindingProbeCheck{Status: "not_checked"},
	}
}

func bindingProbeDeploymentChanged(inventory api.AppBindingInventory, task api.AppTaskResponse) bool {
	if inventory.VerificationDeploymentID != "" && !sameBindingDeployment(task.DeploymentID, inventory.VerificationDeploymentID) {
		return true
	}
	return task.DeploymentScope != "" && inventory.VerificationScope != "" && task.DeploymentScope != inventory.VerificationScope
}

func renderPostgresBindingProbeReport(app string, report api.PostgresBindingProbeReport, task api.AppTaskResponse) {
	_, _ = fmt.Fprintf(osStdout, "PostgreSQL binding canary: %s → %s\n", app, report.EnvironmentKey)
	_, _ = fmt.Fprintf(osStdout, "Task: %s (deployment=%s)\n", task.ID, task.DeploymentID)
	for _, row := range []struct {
		name  string
		check api.PostgresBindingProbeCheck
	}{
		{name: "Environment", check: report.Environment},
		{name: "Configuration", check: report.Configuration},
		{name: "Connection", check: report.Connection},
		{name: "Query", check: report.Query},
	} {
		label := strings.ToUpper(strings.ReplaceAll(row.check.Status, "_", " "))
		if label == "" {
			label = "NOT CHECKED"
		}
		if row.check.Detail != "" {
			_, _ = fmt.Fprintf(osStdout, "%-14s %s (%s)\n", row.name, label, row.check.Detail)
		} else {
			_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", row.name, label)
		}
	}
	if report.Error != "" {
		PrintFail(osStderr, "%s", report.Error)
	}
	if task.OutputTruncated {
		PrintWarn(osStderr, "canary output was truncated at %d bytes", task.MaxOutputBytes)
	}
}

func runServiceBindingProbe(ctx context.Context, client serviceBindingProbeClient, slug, service string, pollInterval, waitTimeout time.Duration) int {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not load app bindings", err)
	}
	bound := false
	for _, binding := range app.ServiceBindings {
		if strings.EqualFold(strings.TrimSpace(binding.Service), service) {
			bound = true
			break
		}
	}
	if !bound {
		return printErr("Service is not bound to this app", fmt.Errorf("%s has no declared binding for %s", slug, service))
	}

	report, task, errorTitle, exitCode, err := executeServiceBindingProbe(ctx, client, slug, service, pollInterval, waitTimeout)
	if errorTitle != "" {
		return printErr(errorTitle, err)
	}
	if exitCode == 130 {
		return exitCode
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return jsonOut(err)
		}
		return exitCode
	}
	renderServiceBindingProbeReport(slug, report, task)
	return exitCode
}

func executeServiceBindingProbe(ctx context.Context, client serviceBindingProbeClient, slug, service string, pollInterval, waitTimeout time.Duration) (api.ServiceBindingProbeReport, api.AppTaskResponse, string, int, error) {
	report := newServiceBindingProbeReport(slug, service)
	request := api.CreateAppTaskRequest{
		Command:        []string{api.AppTaskServiceBindingProbeCommand, service},
		TimeoutSeconds: bindingProbeTaskTimeoutSeconds,
		MaxOutputBytes: 4096,
	}
	managed, selectionErr := selectedManagedServiceProbe(ctx, client, slug, service)
	if selectionErr != nil {
		return report, api.AppTaskResponse{}, "Could not select service-binding canary", 1, selectionErr
	}
	if managed != nil {
		request.Command = []string{api.AppTaskServiceBindingTargetProbeCommand, service, managed.TargetDeploymentID}
		request.VerificationDeploymentID = managed.CallerDeploymentID
	}
	task, err := client.CreateAppTask(ctx, slug, request)
	if err != nil {
		report.Error = err.Error()
		return report, task, "Could not start service-binding canary", 1, err
	}
	report.TaskID = task.ID
	report.DeploymentID = task.DeploymentID
	interruptContext, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	waitContext, cancel := context.WithTimeout(interruptContext, waitTimeout)
	defer cancel()
	for !task.Status.Terminal() {
		select {
		case <-waitContext.Done():
			if bindingProbeAutoCancel(ctx) && errors.Is(interruptContext.Err(), context.Canceled) {
				cancelContext, cancelRequest := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
				defer cancelRequest()
				cancelled, cancelErr := client.CancelAppTask(cancelContext, slug, task.ID)
				if cancelErr != nil {
					PrintWarn(osStderr, "could not request cancellation for canary task %s: %v", task.ID, cancelErr)
				} else if !jsonOutput {
					PrintWarn(osStderr, "cancellation requested for canary task %s (status=%s)", task.ID, cancelled.Status)
				}
				return report, task, "", 130, nil
			}
			report.Error = "canary task did not finish before the wait timeout"
			return report, task, "Service-binding canary is still running", 1, waitContext.Err()
		case <-time.After(pollInterval):
		}
		task, err = client.GetAppTask(waitContext, slug, task.ID)
		if err != nil {
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
				report.Error = "canary task did not finish before the wait timeout"
				return report, task, "Service-binding canary wait timed out; the task is still running", 1, waitContext.Err()
			}
			report.Error = err.Error()
			return report, task, "Could not read service-binding canary status", 1, err
		}
	}

	if task.StdoutTail != "" {
		var taskReport api.ServiceBindingProbeReport
		if err := json.Unmarshal([]byte(task.StdoutTail), &taskReport); err != nil {
			report.Error = "task did not return a valid canary report"
			if task.Failure != nil {
				report.Error += fmt.Sprintf(" (%s: %s)", task.Failure.Code, task.Failure.Message)
			}
		} else {
			report = taskReport
		}
	} else if task.Failure != nil {
		report.Error = task.Failure.Message
	} else {
		report.Error = "task did not return a canary report"
	}
	if report.Service != service {
		report.Error = "canary report does not match the selected service"
	}
	if report.Service == "" {
		report.Service = service
	}
	report.App = slug
	if report.URL == "" {
		report.URL = "https://" + service + ".internal"
	}
	report.TaskID = task.ID
	report.DeploymentID = task.DeploymentID
	if task.OutputTruncated {
		report.Error = "canary output was truncated"
	}
	if task.Status != api.AppTaskStatusSucceeded || !report.Passed() || task.OutputTruncated || report.Error != "" || (task.ExitCode != nil && *task.ExitCode != 0) {
		if report.Error == "" {
			if task.Failure != nil {
				report.Error = task.Failure.Code + ": " + task.Failure.Message
			} else {
				report.Error = "canary task did not succeed"
			}
		}
		return report, task, "", 1, nil
	}
	if managed != nil {
		if err := confirmManagedServiceProbe(ctx, client, slug, *managed, report, task); err != nil {
			report.Error = err.Error()
			return report, task, "", 1, nil
		}
	} else if report.ManagedService != nil {
		report.Error = "managed service context was not selected; use --deployment and retry"
		return report, task, "", 1, nil
	}
	return report, task, "", 0, nil
}

func newServiceBindingProbeReport(app, service string) api.ServiceBindingProbeReport {
	return api.ServiceBindingProbeReport{
		App:           app,
		Service:       service,
		URL:           "https://" + service + ".internal",
		DNS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		TLS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		Authorization: api.ServiceBindingProbeCheck{Status: "not_checked"},
		Routing:       api.ServiceBindingProbeCheck{Status: "not_checked"},
	}
}

func renderServiceBindingProbeBatch(batch serviceBindingProbeBatchReport) {
	if batch.Gate != nil {
		renderAllBindingCoverageGate(batch.Gate)
	}
	_, _ = fmt.Fprintf(osStdout, "Binding verification: %s scope=%s coverage=%s (%d/%d passed, %d failed, %d unsupported, %d skipped, %d not checked)\n", batch.App, batch.Scope, batch.Coverage, batch.Passed, batch.Total, batch.Failed, batch.Unsupported, batch.Skipped, batch.NotChecked)
	for _, item := range batch.Bindings {
		_, _ = fmt.Fprintln(osStdout)
		if item.Report != nil {
			renderServiceBindingProbeReport(batch.App, *item.Report, api.AppTaskResponse{ID: item.Report.TaskID, DeploymentID: item.Report.DeploymentID})
		} else if item.QueueReport != nil {
			_, _ = fmt.Fprintf(osStdout, "Queue binding %s scope=%s: %s (deployment=%s, canary=%s, receipt=%s)\n", item.Binding, item.Scope, item.Status, item.QueueReport.DeploymentID, item.QueueReport.ID, item.QueueReport.Status)
			if item.Error != "" {
				_, _ = fmt.Fprintln(osStdout, item.Error)
			}
		} else if item.OutboundReport != nil {
			report := *item.OutboundReport
			renderOutboundBindingProbeReport(batch.App, report, api.AppTaskResponse{ID: report.TaskID, DeploymentID: report.DeploymentID})
		} else if item.PostgresReport != nil {
			report := *item.PostgresReport
			renderPostgresBindingProbeReport(batch.App, report, api.AppTaskResponse{ID: report.TaskID, DeploymentID: report.DeploymentID})
		} else if item.ObjectStorageReport != nil {
			report := *item.ObjectStorageReport
			renderObjectStorageBindingProbeReport(batch.App, report, api.AppTaskResponse{ID: report.TaskID, DeploymentID: report.DeploymentID})
		} else {
			_, _ = fmt.Fprintf(osStdout, "%s %s (%s) scope=%s: %s (%s)\n", item.Type, item.Name, item.Binding, item.Scope, item.Status, item.Reason)
		}
	}
	for _, issue := range batch.Issues {
		PrintWarn(osStderr, "%s", issue.Message)
	}
}

func renderServiceBindingProbeReport(app string, report api.ServiceBindingProbeReport, task api.AppTaskResponse) {
	_, _ = fmt.Fprintf(osStdout, "Service binding canary: %s → %s\n", app, report.Service)
	_, _ = fmt.Fprintf(osStdout, "Endpoint: %s (deployment=%s)\n", report.URL, task.DeploymentID)
	for _, row := range []struct {
		name  string
		check api.ServiceBindingProbeCheck
	}{
		{name: "DNS", check: report.DNS},
		{name: "TLS", check: report.TLS},
		{name: "Authorization", check: report.Authorization},
		{name: "Routing", check: report.Routing},
	} {
		label := strings.ToUpper(strings.ReplaceAll(row.check.Status, "_", " "))
		if label == "" {
			label = "NOT CHECKED"
		}
		if row.check.Detail != "" {
			_, _ = fmt.Fprintf(osStdout, "%-14s %s (%s)\n", row.name, label, row.check.Detail)
		} else {
			_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", row.name, label)
		}
	}
	if report.Error != "" {
		PrintFail(osStderr, "%s", report.Error)
	}
	if task.Failure != nil && report.Error == "" {
		PrintFail(osStderr, "%s: %s", task.Failure.Code, task.Failure.Message)
	}
	if task.OutputTruncated {
		PrintWarn(osStderr, "canary output was truncated at %d bytes", task.MaxOutputBytes)
	}
}
