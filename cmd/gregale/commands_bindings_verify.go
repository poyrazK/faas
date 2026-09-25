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

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	bindingProbeTaskTimeoutSeconds = 15
	bindingProbeWaitTimeoutDefault = 5 * time.Minute
)

type serviceBindingProbeClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	CreateAppTask(context.Context, string, api.CreateAppTaskRequest) (api.AppTaskResponse, error)
	GetAppTask(context.Context, string, string) (api.AppTaskResponse, error)
	CancelAppTask(context.Context, string, string) (api.AppTaskResponse, error)
}

func cmdBindingsVerify(args []string) int {
	fs := newFlagSet("bindings-verify", flag.ContinueOnError)
	pollInterval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval while the canary runs")
	waitTimeout := fs.Duration("wait-timeout", bindingProbeWaitTimeoutDefault, "maximum time for the CLI to wait for the canary task")
	flagArgs, positionals := splitArgsForFlags(args)
	if err := fs.Parse(flagArgs); err != nil || fs.NArg() != 0 || len(positionals) != 2 {
		printBindingsVerifyUsage()
		return 1
	}
	slug := strings.TrimSpace(positionals[0])
	service := strings.TrimSpace(positionals[1])
	if !api.ValidAppSlug(slug) || *pollInterval <= 0 || *waitTimeout <= 0 {
		printBindingsVerifyUsage()
		return 1
	}
	services, err := api.NormalizeServiceBindingTargets([]string{service})
	if err != nil || len(services) != 1 {
		printBindingsVerifyUsage()
		return 1
	}
	service = services[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return runServiceBindingProbe(context.Background(), client, slug, service, *pollInterval, *waitTimeout)
}

func printBindingsVerifyUsage() {
	PrintUsage(osStderr, "usage: gregale bindings verify <app> <service> [--poll-interval D] [--wait-timeout D]", "bindings")
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

	request := api.CreateAppTaskRequest{
		Command:        []string{api.AppTaskServiceBindingProbeCommand, service},
		TimeoutSeconds: bindingProbeTaskTimeoutSeconds,
		MaxOutputBytes: 4096,
	}
	task, err := client.CreateAppTask(ctx, slug, request)
	if err != nil {
		return printErr("Could not start service-binding canary", err)
	}
	interruptContext, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	waitContext, cancel := context.WithTimeout(interruptContext, waitTimeout)
	defer cancel()
	for !task.Status.Terminal() {
		select {
		case <-waitContext.Done():
			if errors.Is(interruptContext.Err(), context.Canceled) {
				cancelled, cancelErr := client.CancelAppTask(context.Background(), slug, task.ID)
				if cancelErr != nil {
					PrintWarn(osStderr, "could not request cancellation for canary task %s: %v", task.ID, cancelErr)
				} else if !jsonOutput {
					PrintWarn(osStderr, "cancellation requested for canary task %s (status=%s)", task.ID, cancelled.Status)
				}
				return 130
			}
			return printErr("Service-binding canary is still running", waitContext.Err())
		case <-time.After(pollInterval):
		}
		task, err = client.GetAppTask(waitContext, slug, task.ID)
		if err != nil {
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
				return printErr("Service-binding canary wait timed out; the task is still running", waitContext.Err())
			}
			return printErr("Could not read service-binding canary status", err)
		}
	}

	report := api.ServiceBindingProbeReport{
		App:           slug,
		Service:       service,
		URL:           "https://" + service + ".internal",
		DNS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		TLS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		Authorization: api.ServiceBindingProbeCheck{Status: "not_checked"},
		Routing:       api.ServiceBindingProbeCheck{Status: "not_checked"},
	}
	if task.StdoutTail != "" {
		if err := json.Unmarshal([]byte(task.StdoutTail), &report); err != nil {
			report.Error = "task did not return a valid canary report"
			if task.Failure != nil {
				report.Error += fmt.Sprintf(" (%s: %s)", task.Failure.Code, task.Failure.Message)
			}
		}
	} else if task.Failure != nil {
		report.Error = task.Failure.Message
	} else {
		report.Error = "task did not return a canary report"
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
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return jsonOut(err)
		}
		if task.Status != api.AppTaskStatusSucceeded || !report.Passed() {
			return 1
		}
		return 0
	}
	renderServiceBindingProbeReport(slug, report, task)
	if task.Status != api.AppTaskStatusSucceeded || !report.Passed() {
		return 1
	}
	return 0
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
