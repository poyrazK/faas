package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const bindingSmokeTaskTimeoutSeconds = 90

func cmdBindingsSmoke(args []string) int {
	fs := newFlagSet("bindings-smoke", flag.ContinueOnError)
	targetDeployment := fs.String("deployment", "", "exact live target deployment to invoke (required)")
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
	if !api.ValidAppSlug(slug) || err != nil || *pollInterval <= 0 || *waitTimeout <= 0 || *targetDeployment == "" || *path == "" ||
		*expectedStatus < 0 || *expectedStatus > 599 || *expectedStatus > 0 && *expectedStatus < 200 {
		printBindingsSmokeUsage()
		return 1
	}
	targetID, err := uuid.Parse(strings.TrimSpace(*targetDeployment))
	if err != nil || targetID == uuid.Nil {
		return printErr("Invalid target deployment", fmt.Errorf("--deployment must be a deployment UUID"))
	}
	requestURI, err := api.NormalizeServiceBindingSmokePath(*path)
	if err != nil {
		return printErr("Invalid service path", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), slug)
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
	request := api.CreateAppTaskRequest{
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
	task, err := client.CreateAppTask(context.Background(), slug, request)
	if err != nil {
		return printErr("Could not start service-binding smoke task", err)
	}
	interruptContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	waitContext, cancel := context.WithTimeout(interruptContext, *waitTimeout)
	defer cancel()
	for !task.Status.Terminal() {
		select {
		case <-waitContext.Done():
			if errors.Is(interruptContext.Err(), context.Canceled) {
				cancelled, cancelErr := client.CancelAppTask(context.Background(), slug, task.ID)
				if cancelErr != nil {
					PrintWarn(osStderr, "could not request cancellation for smoke task %s: %v", task.ID, cancelErr)
				} else if !jsonOutput {
					PrintWarn(osStderr, "cancellation requested for smoke task %s (status=%s)", task.ID, cancelled.Status)
				}
				return 130
			}
			return printErr("Service-binding smoke task is still running", waitContext.Err())
		case <-time.After(*pollInterval):
		}
		task, err = client.GetAppTask(waitContext, slug, task.ID)
		if err != nil {
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
				return printErr("Service-binding smoke wait timed out; the task may still be running", waitContext.Err())
			}
			return printErr("Could not read service-binding smoke task status", err)
		}
	}
	if task.OutputTruncated {
		PrintWarn(osStderr, "smoke task output was truncated at %d bytes", task.MaxOutputBytes)
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
	if task.StdoutTail != "" {
		if err := json.Unmarshal([]byte(task.StdoutTail), &report); err != nil {
			report.Error = "task did not return a valid smoke report"
			if task.Failure != nil {
				report.Error += fmt.Sprintf(" (%s: %s)", task.Failure.Code, task.Failure.Message)
			}
		}
	} else if task.Failure != nil {
		report.Error = task.Failure.Message
	} else {
		report.Error = "task did not return a smoke report"
	}
	// Keep CLI-owned identity fields authoritative and never echo a user query
	// string back in the report.
	report.App = slug
	report.TaskID = task.ID
	report.CallerDeploymentID = task.DeploymentID
	report.TargetDeploymentID = targetID.String()
	report.URL = "https://" + service + ".internal"
	report.Service = service
	report.Path = smokeReportPath(requestURI)
	if report.ExpectedStatus == "" {
		report.ExpectedStatus = expectedServiceStatus(*expectedStatus)
	}
	passed := task.Status == api.AppTaskStatusSucceeded && report.Passed
	if !passed && report.Error == "" {
		if task.Failure != nil {
			report.Error = task.Failure.Code + ": " + task.Failure.Message
		} else {
			report.Error = "smoke task did not succeed"
		}
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return jsonOut(err)
		}
	} else {
		renderServiceBindingSmokeReport(report)
	}
	if passed {
		return 0
	}
	return 1
}

func normalizeOneServiceBindingTarget(raw string) (string, error) {
	services, err := api.NormalizeServiceBindingTargets([]string{strings.TrimSpace(raw)})
	if err != nil || len(services) != 1 {
		return "", fmt.Errorf("service name is invalid")
	}
	return services[0], nil
}

func printBindingsSmokeUsage() {
	PrintUsage(osStderr, "usage: gregale bindings smoke <app> <service> --deployment <id> --path </path> [--expect-status <code>] [flags]", "bindings")
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
