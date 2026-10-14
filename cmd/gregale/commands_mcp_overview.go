package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpOverviewOptions struct {
	App, Worker, Observer, Endpoint string
	TokenEnv, Policy                string
	Timeout                         time.Duration
	Roles                           []mcpReleaseRole
}

type mcpOverviewAction struct {
	Label     string   `json:"label"`
	Arguments []string `json:"arguments"`
	Mutating  bool     `json:"mutating"`
}

type mcpOverviewIssue struct {
	Component string             `json:"component"`
	Code      string             `json:"code"`
	Severity  string             `json:"severity"`
	Message   string             `json:"message"`
	Action    *mcpOverviewAction `json:"action,omitempty"`
}

type mcpOverviewServing struct {
	State   string                  `json:"state"`
	Catalog *mcpCatalogStatusReport `json:"catalog,omitempty"`
}

type mcpOverviewObserver struct {
	Readiness            mcpTasksObserverReadiness `json:"readiness"`
	ProcessDetected      bool                      `json:"process_detected"`
	NamespaceMatches     bool                      `json:"namespace_matches"`
	MetricsTargetMatches bool                      `json:"metrics_target_matches"`
	HeartbeatFresh       bool                      `json:"heartbeat_fresh"`
}

type mcpOverviewTasks struct {
	State           string                `json:"state"`
	App             string                `json:"app,omitempty"`
	RunningReplicas *int                  `json:"running_replicas,omitempty"`
	Summary         *mcpTasksStatusResult `json:"summary,omitempty"`
	Observer        *mcpOverviewObserver  `json:"observer,omitempty"`
}

type mcpOverviewHold struct {
	App                string             `json:"app"`
	DeploymentID       string             `json:"deployment_id"`
	Code               string             `json:"code"`
	HeldAt             time.Time          `json:"held_at"`
	AgeSeconds         *float64           `json:"age_seconds,omitempty"`
	RecoveryAttempts   int                `json:"recovery_attempts"`
	RecoveryInProgress bool               `json:"recovery_in_progress"`
	NextRetryAt        *time.Time         `json:"next_retry_at,omitempty"`
	Inspect            mcpOverviewAction  `json:"inspect"`
	Retry              *mcpOverviewAction `json:"retry,omitempty"`
}

type mcpOverviewReleases struct {
	Admissions               []mcpAdmissionHistory `json:"admissions,omitempty"`
	AdmissionDeploymentLimit int                   `json:"admission_deployment_limit,omitempty"`
	State                    string                `json:"state"`
	Complete                 bool                  `json:"complete"`
	Holds                    []mcpOverviewHold     `json:"holds"`
}

type mcpOverviewReport struct {
	Convergence *mcpConvergenceReport `json:"convergence,omitempty"`
	Version     int                   `json:"version"`
	App         string                `json:"app"`
	ObservedAt  time.Time             `json:"observed_at"`
	Health      string                `json:"health"`
	Serving     mcpOverviewServing    `json:"serving"`
	Tasks       mcpOverviewTasks      `json:"tasks"`
	Releases    mcpOverviewReleases   `json:"releases"`
	Diagnostics []mcpOverviewIssue    `json:"diagnostics"`
}

func cmdMCPOverview(args []string) int { return cmdMCPOverviewMode(args, false) }

func cmdMCPOverviewMode(args []string, doctor bool) int {
	fs := newFlagSet("mcp-overview", flag.ContinueOnError)
	app := fs.String("app", "", "serving MCP application slug")
	worker := fs.String("worker-app", "", "optional separate durable Task worker application")
	observer := fs.String("observer-app", "", "observer to inspect (defaults to the worker scaling link)")
	endpoint := fs.String("endpoint", "/mcp", "literal MCP endpoint path")
	tokenEnv := fs.String("token-env", "", "MCP client bearer token environment variable")
	policy := fs.String("release-policy", "", "role-specific reviewed catalog baselines")
	timeout := fs.Duration("timeout", 30*time.Second, "overall inspection deadline, at most 2m")
	var source *string
	if doctor {
		source = fs.String("path", "", "optional source directory with reviewed gregale-mcp.json")
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	options := mcpOverviewOptions{App: *app, Worker: *worker, Observer: *observer, Endpoint: *endpoint, Timeout: *timeout, TokenEnv: *tokenEnv, Policy: *policy}
	if fs.NArg() != 0 || !api.ValidAppSlug(*app) || *timeout <= 0 || *timeout > 2*time.Minute || !api.ValidMCPProbeEndpointPath(*endpoint) || (*worker != "" && (!api.ValidAppSlug(*worker) || *worker == *app)) || (*observer != "" && (*worker == "" || !api.ValidAppSlug(*observer) || *observer == *app || *observer == *worker)) {
		return printErr("Invalid MCP overview flags", errors.New("supply a valid --app, distinct optional --worker-app/--observer-app, a literal --endpoint and a positive --timeout up to 2m"))
	}
	if *policy != "" && *tokenEnv != "" {
		return printErr("Invalid MCP overview auth flags", errors.New("choose --token-env or --release-policy"))
	}
	var err error
	if *policy != "" {
		options.Roles, err = loadMCPReleasePolicy(*policy)
	} else {
		var token string
		token, err = mcpToken(*tokenEnv)
		options.Roles = []mcpReleaseRole{{Name: "default", token: token}}
	}
	if err != nil {
		return printErr("MCP overview authentication", err)
	}
	var config *mcphosting.Config
	if source != nil && *source != "" {
		loaded, loadErr := mcphosting.Load(*source)
		if loadErr != nil {
			return printErr("MCP doctor configuration", errors.New("could not load a valid gregale-mcp.json; check the source directory and configuration"))
		}
		if loaded.Endpoint != options.Endpoint {
			return printErr("MCP doctor configuration", errors.New("--endpoint must match the source configuration"))
		}
		config = &loaded
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if doctor {
		return writeMCPStackDoctor(collectMCPStackDoctor(ctx, client, options, config))
	}
	return writeMCPOverview(collectMCPOverview(ctx, client, options))
}

type mcpOverviewCollection struct {
	Convergence      *mcpConvergenceReport
	Admissions       []mcpAdmissionHistory
	Serving          mcpOverviewServing
	Tasks            mcpOverviewTasks
	Holds            []mcpOverviewHold
	ReleasesComplete bool
	Issues           []mcpOverviewIssue
}

// Serving and worker inspection have independent failure paths and share one
// deadline. Neither collector mutates workloads or invokes application tools.
func collectMCPOverview(ctx context.Context, client *Client, options mcpOverviewOptions) mcpOverviewReport {
	serving := make(chan mcpOverviewCollection, 1)
	workers := make(chan mcpOverviewCollection, 1)
	go func() { serving <- collectMCPOverviewServing(ctx, client, options) }()
	go func() { workers <- collectMCPOverviewTasks(ctx, client, options) }()
	web, worker := <-serving, <-workers
	report := mcpOverviewReport{Convergence: web.Convergence, Version: 1, App: options.App, ObservedAt: time.Now().UTC(), Health: "healthy", Serving: web.Serving, Tasks: worker.Tasks,
		Releases:    mcpOverviewReleases{Admissions: web.Admissions, AdmissionDeploymentLimit: 5, State: "healthy", Complete: web.ReleasesComplete && worker.ReleasesComplete, Holds: append(web.Holds, worker.Holds...)},
		Diagnostics: append(web.Issues, worker.Issues...)}
	if report.Releases.Holds == nil {
		report.Releases.Holds = []mcpOverviewHold{}
	}
	if report.Diagnostics == nil {
		report.Diagnostics = []mcpOverviewIssue{}
	}
	if !report.Releases.Complete {
		report.Releases.State = "unknown"
	}
	if len(report.Releases.Holds) > 0 {
		report.Releases.State = "blocked"
	}
	for _, state := range []string{report.Serving.State, report.Tasks.State, report.Releases.State} {
		if state == "degraded" || state == "blocked" {
			report.Health = "degraded"
			break
		}
		if state == "unknown" {
			report.Health = "unknown"
		}
	}
	if report.Convergence != nil {
		switch report.Convergence.State {
		case "pending":
			report.Health = "degraded"
		case "unknown", "unverified":
			if report.Health == "healthy" {
				report.Health = "unknown"
			}
		}
	}
	return report
}

func mcpOverviewInspection(label string, args ...string) *mcpOverviewAction {
	return &mcpOverviewAction{Label: label, Arguments: append([]string{"gregale"}, args...)}
}

func writeMCPOverview(report mcpOverviewReport) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "MCP overview for %s: %s\n", report.App, report.Health)
		writeMCPConvergence(report.Convergence)
		_, _ = fmt.Fprintf(osStdout, "  Serving: %s\n", report.Serving.State)
		if catalog := report.Serving.Catalog; catalog != nil {
			_, _ = fmt.Fprintf(osStdout, "  Serving replicas: %d/%d catalog checks; %d/%d protocol checks\n", catalog.CheckedReplicas, catalog.ExpectedReplicas, catalog.ProtocolCheckedReplicas, catalog.ProtocolExpectedReplicas)
		}
		for _, history := range report.Releases.Admissions {
			_ = writeMCPAdmissionHistory(history)
		}
		_, _ = fmt.Fprintf(osStdout, "  Tasks: %s", report.Tasks.State)
		if report.Tasks.App != "" {
			_, _ = fmt.Fprintf(osStdout, " (%s)", report.Tasks.App)
		}
		_, _ = fmt.Fprintln(osStdout)
		if report.Tasks.RunningReplicas != nil {
			_, _ = fmt.Fprintf(osStdout, "  Running worker replicas: %d\n", *report.Tasks.RunningReplicas)
		}
		if summary := report.Tasks.Summary; summary != nil {
			for _, metric := range summary.Metrics {
				state := "unknown"
				if metric.Present && !metric.Stale {
					state = "fresh"
				}
				if state == "fresh" {
					_, _ = fmt.Fprintf(osStdout, "  %s: %.2f (%s)\n", metric.Name, metric.Value, state)
				} else {
					_, _ = fmt.Fprintf(osStdout, "  %s: %s\n", metric.Name, state)
				}
			}
		}
		if observer := report.Tasks.Observer; observer != nil {
			_, _ = fmt.Fprintf(osStdout, "  Observer: %s (ready=%t, linked=%t, heartbeat fresh=%t)\n", observer.Readiness.AppSlug, observer.Readiness.Ready, observer.Readiness.Linked, observer.HeartbeatFresh)
		}
		_, _ = fmt.Fprintf(osStdout, "  Releases: %s (%d promotion holds, inspection complete=%t)\n", report.Releases.State, len(report.Releases.Holds), report.Releases.Complete)
		for _, issue := range report.Diagnostics {
			_, _ = fmt.Fprintf(osStdout, "  %s/%s: %s\n", issue.Component, issue.Code, mcpSafeTerminalText(issue.Message))
			if issue.Action != nil {
				_, _ = fmt.Fprintf(osStdout, "    %s\n", mcpOverviewCommand(*issue.Action))
			}
		}
		for _, hold := range report.Releases.Holds {
			_, _ = fmt.Fprintf(osStdout, "  Held deployment %s: %s; recovery attempts=%d, in progress=%t\n    %s\n", mcpSafeTerminalText(hold.DeploymentID), mcpSafeTerminalText(hold.Code), hold.RecoveryAttempts, hold.RecoveryInProgress, mcpOverviewCommand(hold.Inspect))
			if hold.Retry != nil {
				_, _ = fmt.Fprintf(osStdout, "    After resolving compatibility: %s\n", mcpOverviewCommand(*hold.Retry))
			}
		}
	}
	if report.Health != "healthy" {
		return 1
	}
	return 0
}

func mcpOverviewCommand(action mcpOverviewAction) string {
	parts := make([]string, len(action.Arguments))
	for i, arg := range action.Arguments {
		parts[i] = recoveryArgument(mcpSafeTerminalText(arg))
	}
	return strings.Join(parts, " ")
}
