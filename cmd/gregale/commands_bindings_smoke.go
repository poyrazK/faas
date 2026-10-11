package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const bindingSmokeTaskTimeoutSeconds = 90

func cmdBindingsSmoke(args []string) int {
	fs := newFlagSet("bindings-smoke", flag.ContinueOnError)
	targetDeployment := fs.String("target-deployment", "", "exact live target deployment UUID to invoke (required)")
	legacyTarget := fs.String("deployment", "", "alias for --target-deployment")
	callerDeployment := fs.String("caller-deployment", "", "exact live caller deployment ID or vN, including zero-traffic candidates")
	path := fs.String("path", "", "absolute path on the target service (required)")
	expectedStatus := fs.Int("expect-status", 0, "require this exact HTTP status; default accepts any 2xx response")
	pollInterval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval while the smoke task runs")
	waitTimeout := fs.Duration("wait-timeout", bindingProbeWaitTimeoutDefault, "maximum time for the CLI to wait for the smoke task")
	flagArgs, positionals := splitArgsForFlags(args)
	if err := fs.Parse(flagArgs); err != nil || fs.NArg() != 0 || len(positionals) != 2 {
		printBindingsSmokeUsage()
		return 1
	}
	slug := strings.TrimSpace(positionals[0])
	service, err := normalizeOneServiceBindingTarget(positionals[1])
	if *targetDeployment == "" {
		*targetDeployment = *legacyTarget
	} else if *legacyTarget != "" && !sameBindingDeployment(*targetDeployment, *legacyTarget) {
		return printErr("Conflicting target deployments", fmt.Errorf("--deployment and --target-deployment must identify the same deployment"))
	}
	if !api.ValidAppSlug(slug) || err != nil || *pollInterval <= 0 || *waitTimeout <= 0 || *targetDeployment == "" || *path == "" ||
		*expectedStatus < 0 || *expectedStatus > 599 || *expectedStatus > 0 && *expectedStatus < 200 || !validBindingDeploymentFlag(*callerDeployment) {
		printBindingsSmokeUsage()
		return 1
	}
	targetID, err := uuid.Parse(strings.TrimSpace(*targetDeployment))
	if err != nil || targetID == uuid.Nil {
		return printErr("Invalid target deployment", fmt.Errorf("--target-deployment must be a deployment UUID"))
	}
	requestURI, err := api.NormalizeServiceBindingSmokePath(*path)
	if err != nil {
		return printErr("Invalid service path", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	interruptContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	waitContext, cancel := context.WithTimeout(interruptContext, *waitTimeout)
	defer cancel()
	app, err := client.GetApp(waitContext, slug)
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
	callerID, err := resolveBindingDeployment(waitContext, client, slug, *callerDeployment)
	if err != nil {
		return printErr("Could not resolve caller deployment", err)
	}
	callerScope := ""
	if callerID != "" {
		deployment, err := client.GetDeployment(waitContext, callerID)
		// MemStore retains historical 32-hex IDs; PostgreSQL accepts either
		// representation. Retry only a 404 and preserve the exact UUID.
		var problem *api.APIError
		if errors.As(err, &problem) && problem.Problem.Status == http.StatusNotFound {
			deployment, err = client.GetDeployment(waitContext, strings.ReplaceAll(callerID, "-", ""))
		}
		if err != nil {
			return printErr("Could not read caller deployment", err)
		}
		if !sameBindingDeployment(deployment.ID, callerID) || deployment.AppID != app.ID || deployment.Status != "live" || deployment.ImageDigest == "" {
			return printErr("Caller deployment unavailable", fmt.Errorf("server did not confirm a live caller deployment belonging to this app"))
		}
		callerScope = deployment.Scope
		if callerScope == "" {
			callerScope = "default"
		}
	}
	request := api.CreateAppTaskRequest{
		SmokeDeploymentID: callerID,
		Command: []string{
			api.AppTaskServiceBindingSmokeCommand,
			service,
			targetID.String(),
			requestURI,
			strconv.Itoa(*expectedStatus),
		},
		TimeoutSeconds: bindingSmokeTaskTimeoutSeconds,
		MaxOutputBytes: 4096,
	}
	task, err := client.CreateAppTask(waitContext, slug, request)
	if err != nil {
		return printErr("Could not start service-binding smoke task", err)
	}
	if err := validateSmokeTask(task, "", app.ID, callerID, callerScope, request.Command); err != nil {
		cancelSmokeTask(waitContext, client, slug, task)
		return printErr("Server did not confirm the smoke task selection", err)
	}
	taskID, admittedCallerID, admittedScope := task.ID, task.DeploymentID, task.DeploymentScope
	for !task.Status.Terminal() {
		select {
		case <-waitContext.Done():
			if errors.Is(interruptContext.Err(), context.Canceled) {
				cancelSmokeTask(waitContext, client, slug, task)
				return 130
			}
			return printErr("Service-binding smoke task is still running", waitContext.Err())
		case <-time.After(*pollInterval):
		}
		task, err = client.GetAppTask(waitContext, slug, taskID)
		if err != nil {
			if errors.Is(interruptContext.Err(), context.Canceled) {
				cancelSmokeTask(waitContext, client, slug, api.AppTaskResponse{ID: taskID})
				return 130
			}
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
				return printErr("Service-binding smoke wait timed out; the task may still be running", waitContext.Err())
			}
			return printErr("Could not read service-binding smoke task status", err)
		}
		if err := validateSmokeTask(task, taskID, app.ID, admittedCallerID, admittedScope, request.Command); err != nil {
			cancelSmokeTask(waitContext, client, slug, api.AppTaskResponse{ID: taskID})
			return printErr("Smoke task selection changed", err)
		}
	}
	report := api.ServiceBindingSmokeReport{
		App:                slug,
		Service:            service,
		TargetDeploymentID: targetID.String(),
		URL:                "https://" + service + ".internal",
		Path:               smokeReportPath(requestURI),
		ExpectedStatus:     expectedServiceStatus(*expectedStatus),
		TaskID:             task.ID,
		CallerDeploymentID: task.DeploymentID,
	}
	completeSmokeReport(&report, task, *expectedStatus)
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return jsonOut(err)
		}
	} else {
		renderServiceBindingSmokeReport(report)
	}
	if report.Passed {
		return 0
	}
	return 1
}

func validateSmokeTask(task api.AppTaskResponse, id, appID, callerID, scope string, command []string) error {
	deployment, err := uuid.Parse(task.DeploymentID)
	if task.ID == "" || id != "" && task.ID != id || task.AppID != appID || err != nil || deployment == uuid.Nil ||
		callerID != "" && !sameBindingDeployment(task.DeploymentID, callerID) || task.DeploymentScope == "" || scope != "" && task.DeploymentScope != scope ||
		task.Kind != api.AppTaskKindManual || task.CommandShell || !slices.Equal(task.Command, command) {
		return fmt.Errorf("task receipt does not match the requested app, caller deployment, scope or command")
	}
	return nil
}

func cancelSmokeTask(ctx context.Context, client *api.Client, slug string, task api.AppTaskResponse) {
	if task.ID == "" || task.Status.Terminal() {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
	defer cancel()
	if _, err := client.CancelAppTask(cancelCtx, slug, task.ID); err != nil {
		PrintWarn(osStderr, "could not request cancellation for smoke task %s: %v", task.ID, err)
	}
}

func completeSmokeReport(report *api.ServiceBindingSmokeReport, task api.AppTaskResponse, expectedStatus int) {
	var guest api.ServiceBindingSmokeReport
	switch {
	case task.OutputTruncated:
		report.Error = "smoke task output was truncated"
	case task.StdoutTail == "" || json.Unmarshal([]byte(task.StdoutTail), &guest) != nil:
		report.Error = "task did not return a valid smoke report"
	case guest.Service != report.Service || !sameBindingDeployment(guest.TargetDeploymentID, report.TargetDeploymentID) || guest.URL != report.URL ||
		guest.Path != report.Path || guest.ExpectedStatus != report.ExpectedStatus ||
		guest.App != "" && guest.App != report.App || guest.TaskID != "" && guest.TaskID != report.TaskID ||
		guest.CallerDeploymentID != "" && !sameBindingDeployment(guest.CallerDeploymentID, report.CallerDeploymentID):
		report.Error = "smoke report does not match the requested service, deployments, path or status policy"
	default:
		report.HTTPStatus, report.ElapsedMillis = guest.HTTPStatus, guest.ElapsedMillis
		statusMatches := guest.HTTPStatus >= 200 && guest.HTTPStatus < 300
		if expectedStatus != 0 {
			statusMatches = guest.HTTPStatus == expectedStatus
		}
		if task.Status != api.AppTaskStatusSucceeded || task.ExitCode == nil || *task.ExitCode != 0 || task.Failure != nil || !guest.Passed || guest.Error != "" || !statusMatches {
			report.Error = "smoke task did not succeed with the expected HTTP status"
			return
		}
		report.Passed = true
	}
}

func normalizeOneServiceBindingTarget(raw string) (string, error) {
	services, err := api.NormalizeServiceBindingTargets([]string{strings.TrimSpace(raw)})
	if err != nil || len(services) != 1 {
		return "", fmt.Errorf("service name is invalid")
	}
	return services[0], nil
}

func printBindingsSmokeUsage() {
	PrintUsage(osStderr, "usage: gregale bindings smoke <app> <service> --target-deployment <id> --path </path> [--caller-deployment <id|vN>] [--expect-status <code>] [flags]", "bindings")
}

func expectedServiceStatus(code int) string {
	if code == 0 {
		return "2xx"
	}
	return strconv.Itoa(code)
}

func smokeReportPath(requestURI string) string {
	parsed, err := api.NormalizeServiceBindingSmokePath(requestURI)
	if err != nil {
		return ""
	}
	path, _, _ := strings.Cut(parsed, "?")
	if path == "" {
		return "/"
	}
	return path
}

func renderServiceBindingSmokeReport(report api.ServiceBindingSmokeReport) {
	if report.HTTPStatus > 0 {
		_, _ = fmt.Fprintf(osStdout, "Service binding smoke: %s → %s (%s)\n", report.App, report.Service, report.TargetDeploymentID)
		_, _ = fmt.Fprintf(osStdout, "  GET %s%s → HTTP %d (expected %s, %d ms)\n", report.URL, report.Path, report.HTTPStatus, report.ExpectedStatus, report.ElapsedMillis)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Service binding smoke: %s → %s (%s)\n", report.App, report.Service, report.TargetDeploymentID)
		_, _ = fmt.Fprintf(osStdout, "  GET %s%s (expected %s)\n", report.URL, report.Path, report.ExpectedStatus)
	}
	if report.Passed {
		_, _ = fmt.Fprintln(osStdout, "  result: passed")
	} else {
		_, _ = fmt.Fprintf(osStdout, "  result: failed — %s\n", report.Error)
	}
	_, _ = fmt.Fprintf(osStdout, "  caller deployment: %s; task: %s\n", report.CallerDeploymentID, report.TaskID)
}
