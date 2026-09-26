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
	bindingProbeCancelTimeout      = 5 * time.Second
)

type serviceBindingProbeClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
	CreateAppTask(context.Context, string, api.CreateAppTaskRequest) (api.AppTaskResponse, error)
	GetAppTask(context.Context, string, string) (api.AppTaskResponse, error)
	CancelAppTask(context.Context, string, string) (api.AppTaskResponse, error)
}

type serviceBindingProbeBatchItem struct {
	Service string                        `json:"service"`
	Status  string                        `json:"status"`
	Report  api.ServiceBindingProbeReport `json:"report"`
}

type serviceBindingProbeBatchReport struct {
	App      string                         `json:"app"`
	Total    int                            `json:"total"`
	Checked  int                            `json:"checked"`
	Passed   int                            `json:"passed"`
	Failed   int                            `json:"failed"`
	Skipped  int                            `json:"skipped"`
	Bindings []serviceBindingProbeBatchItem `json:"bindings"`
}

func cmdBindingsVerify(args []string) int {
	fs := newFlagSet("bindings-verify", flag.ContinueOnError)
	all := fs.Bool("all", false, "verify every declared service binding")
	pollInterval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval while the canary runs")
	waitTimeout := fs.Duration("wait-timeout", bindingProbeWaitTimeoutDefault, "maximum time for the CLI to wait for canary task(s)")
	flagArgs, positionals := splitArgsForFlags(args)
	if err := fs.Parse(flagArgs); err != nil || fs.NArg() != 0 || *all && len(positionals) != 1 || !*all && len(positionals) != 2 {
		printBindingsVerifyUsage()
		return 1
	}
	slug := strings.TrimSpace(positionals[0])
	if !api.ValidAppSlug(slug) || *pollInterval <= 0 || *waitTimeout <= 0 {
		printBindingsVerifyUsage()
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *all {
		return runAllServiceBindingProbes(context.Background(), client, slug, *pollInterval, *waitTimeout)
	}
	service := strings.TrimSpace(positionals[1])
	services, err := api.NormalizeServiceBindingTargets([]string{service})
	if err != nil || len(services) != 1 {
		printBindingsVerifyUsage()
		return 1
	}
	service = services[0]
	return runServiceBindingProbe(context.Background(), client, slug, service, *pollInterval, *waitTimeout)
}

func printBindingsVerifyUsage() {
	PrintUsage(osStderr, "usage: gregale bindings verify <app> <service> [flags] | gregale bindings verify <app> --all [flags]", "bindings")
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

func runAllServiceBindingProbes(ctx context.Context, client serviceBindingProbeClient, slug string, pollInterval, waitTimeout time.Duration) int {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not load app bindings", err)
	}
	declared := make([]string, 0, len(app.ServiceBindings))
	for _, binding := range app.ServiceBindings {
		declared = append(declared, binding.Service)
	}
	services, err := api.NormalizeServiceBindingTargets(declared)
	if err != nil {
		return printErr("Could not read app bindings", err)
	}
	if len(services) == 0 {
		return printErr("No service bindings to verify", fmt.Errorf("%s has no declared service bindings", slug))
	}

	batch := serviceBindingProbeBatchReport{
		App:      slug,
		Total:    len(services),
		Bindings: make([]serviceBindingProbeBatchItem, 0, len(services)),
	}
	batchContext, cancelBatch := context.WithTimeout(ctx, waitTimeout)
	defer cancelBatch()
	interrupted := false
	for _, service := range services {
		if interrupted || batchContext.Err() != nil {
			report := newServiceBindingProbeReport(slug, service)
			if interrupted {
				report.Error = "not checked because verification was interrupted"
			} else {
				report.Error = "not checked before the overall wait timeout"
			}
			batch.Skipped++
			batch.Bindings = append(batch.Bindings, serviceBindingProbeBatchItem{Service: service, Status: "not_checked", Report: report})
			continue
		}
		report, task, errorTitle, exitCode, probeErr := executeServiceBindingProbe(batchContext, client, slug, service, pollInterval, waitTimeout)
		if errorTitle != "" {
			report = newServiceBindingProbeReport(slug, service)
			report.Error = probeErr.Error()
		}
		status := "failed"
		switch exitCode {
		case 0:
			status = "passed"
			batch.Passed++
		case 130:
			status = "not_checked"
			batch.Skipped++
			interrupted = true
			report.Error = "verification interrupted before a result was collected"
		default:
			batch.Failed++
			batch.Checked++
		}
		batch.Bindings = append(batch.Bindings, serviceBindingProbeBatchItem{
			Service: service,
			Status:  status,
			Report:  report,
		})
		if exitCode == 0 {
			batch.Checked++
		}
		if task.OutputTruncated {
			PrintWarn(osStderr, "canary output for %s was truncated at %d bytes", service, task.MaxOutputBytes)
		}
	}
	if jsonOutput {
		if err := writeJSON(batch); err != nil {
			return jsonOut(err)
		}
	} else {
		renderServiceBindingProbeBatch(batch)
	}
	if interrupted {
		return 130
	}
	if batch.Failed > 0 || batch.Skipped > 0 {
		return 1
	}
	return 0
}

func executeServiceBindingProbe(ctx context.Context, client serviceBindingProbeClient, slug, service string, pollInterval, waitTimeout time.Duration) (api.ServiceBindingProbeReport, api.AppTaskResponse, string, int, error) {
	report := newServiceBindingProbeReport(slug, service)
	request := api.CreateAppTaskRequest{
		Command:        []string{api.AppTaskServiceBindingProbeCommand, service},
		TimeoutSeconds: bindingProbeTaskTimeoutSeconds,
		MaxOutputBytes: 4096,
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
			if errors.Is(interruptContext.Err(), context.Canceled) {
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
	if task.Status != api.AppTaskStatusSucceeded || !report.Passed() {
		if report.Error == "" {
			if task.Failure != nil {
				report.Error = task.Failure.Code + ": " + task.Failure.Message
			} else {
				report.Error = "canary task did not succeed"
			}
		}
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
	_, _ = fmt.Fprintf(osStdout, "Service binding readiness: %s (%d/%d passed, %d failed, %d not checked)\n", batch.App, batch.Passed, batch.Total, batch.Failed, batch.Skipped)
	for _, item := range batch.Bindings {
		_, _ = fmt.Fprintln(osStdout)
		renderServiceBindingProbeReport(batch.App, item.Report, api.AppTaskResponse{
			ID: item.Report.TaskID, DeploymentID: item.Report.DeploymentID,
		})
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
