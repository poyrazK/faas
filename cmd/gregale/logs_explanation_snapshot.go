package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type explanationPattern struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
}

type logExplanationSummary struct {
	ErrorCount  int                  `json:"error_count"`
	WarnCount   int                  `json:"warn_count"`
	InfoCount   int                  `json:"info_count"`
	LastError   string               `json:"last_error,omitempty"`
	TopPatterns []explanationPattern `json:"top_patterns"`
	Shortened   bool                 `json:"messages_shortened"`
}

type deploymentLogExplanation struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	App           string `json:"app_slug"`
	Deployment    string `json:"deployment_id"`
	Status        string `json:"deployment_status"`
	Source        string `json:"source"`
	FailureReason string `json:"failure_reason,omitempty"`
	FailureCode   string `json:"failure_code,omitempty"`
	FailureHint   string `json:"failure_hint,omitempty"`
	logExplanationSummary
	ObservedLines  int      `json:"observed_lines"`
	SkippedRecords int      `json:"skipped_records"`
	EntryLimit     int      `json:"entry_limit"`
	Complete       bool     `json:"complete"`
	EndReason      string   `json:"end_reason"`
	ReadError      string   `json:"read_error,omitempty"`
	ReadErrorCode  string   `json:"read_error_code,omitempty"`
	ReadStatus     int      `json:"read_status,omitempty"`
	Unclassified   int      `json:"unclassified_count"`
	Warnings       []string `json:"warnings"`
	NextCommands   []string `json:"next_commands"`
}

func validateJSONLogExplanation(fs *flag.FlagSet, deployment string, follow bool) error {
	if strings.TrimSpace(deployment) == "" || follow {
		return errors.New("--explain --json requires a bounded --deployment ID (or --release ID) snapshot without --follow")
	}
	allowed := map[string]bool{"app": true, "deployment": true, "release": true, "explain": true, "source": true, "dry-run": true, "follow": true}
	var unsupported []string
	fs.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			unsupported = append(unsupported, "--"+f.Name)
		}
	})
	if len(unsupported) > 0 {
		return fmt.Errorf("--explain --json inspects an unfiltered deployment snapshot; unsupported flags: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

func runDeploymentLogExplanation(ctx context.Context, client *Client, slug, id string) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.CLILogExplanationTimeout)
	defer cancel()
	resolved, err := resolveDeploymentRef(ctx, client, slug, id)
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	id = resolved
	dep, err := client.GetDeployment(ctx, id)
	if err != nil {
		return printErr("Could not read deployment", err)
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not verify deployment app", err)
	}
	if !strings.EqualFold(dep.ID, id) || app.ID == "" || dep.AppID != app.ID {
		return printErr("Deployment target mismatch", errors.New("deployment does not belong to the selected app; no log stream was opened"))
	}
	collector := newExplainCollector(slug, dep.ID)
	report := deploymentLogExplanation{SchemaVersion: 1, Type: "explanation", App: slug, Deployment: dep.ID, Status: dep.Status,
		EntryLimit: api.CLILogExplanationEntries, EndReason: "eof", Warnings: []string{},
		NextCommands: []string{recoveryCLI() + " release get " + recoveryArgument(dep.ID), recoveryCLI() + " logs " + recoveryArgument(slug) + " --deployment " + recoveryArgument(dep.ID)}}
	clean := collector.message
	report.FailureReason, report.FailureCode, report.FailureHint = clean(dep.Error), clean(dep.ErrorCode), clean(dep.ErrorHint)
	var body io.ReadCloser
	switch dep.Status {
	case deploymentStatusFailed, "cancelled", "pending", "building", "imaging", "snapshotting":
		report.Source = "build/deploy"
		body, err = client.StreamDeploymentLogs(ctx, dep.ID, nil, api.CLILogExplanationEntries, false)
	default:
		report.Source = "runtime"
		body, err = client.StreamAppLogs(ctx, slug, dep.ID, false, api.LogFilter{})
	}
	code := 0
	if err != nil {
		report.EndReason, report.ReadError, code = "open_failed", clean(err.Error()), 1
		var problem *APIError
		if errors.As(err, &problem) {
			report.ReadErrorCode, report.ReadStatus = clean(problem.Problem.Code), problem.Problem.Status
		}
	} else {
		defer func() { _ = body.Close() }()
		code = readDeploymentLogExplanation(ctx, body, collector, &report)
	}
	if ctx.Err() != nil {
		report.Complete = false
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			report.EndReason, code = "deadline", 3
		} else {
			report.EndReason, code = "cancelled", 130
		}
	}
	report.logExplanationSummary = collector.summary()
	report.Unclassified = max(0, report.ObservedLines-report.ErrorCount-report.WarnCount-report.InfoCount)
	report.Warnings = append(report.Warnings, "Counts describe returned retained lines only; filters, retention and server-side truncation may omit earlier output. Deployment status is the metadata read before the snapshot.")
	if !report.Complete && code == 0 {
		code = 3
	}
	if result := jsonOut(writeJSON(report)); result != 0 {
		return result
	}
	return code
}

func readDeploymentLogExplanation(ctx context.Context, body io.ReadCloser, collector *explainCollector, report *deploymentLogExplanation) int {
	decoder := api.NewDecoder(body)
	decoder.SetCloseFn(body.Close)
	defer func() { _ = decoder.Close() }()
	partial := false
	seen := 0
	code, streamErr := consumeLogStream(ctx, decoder.Events(), decoder.Errors(), func(event api.Event) (bool, int) {
		switch event.Event {
		case "log":
			seen++
			if seen > api.CLILogExplanationEntries {
				report.EndReason = "entry_limit"
				return true, 3
			}
			if strings.HasPrefix(strings.TrimSpace(event.Data), "{") {
				var record explainLogRecord
				if json.Unmarshal([]byte(event.Data), &record) != nil || record.Line == nil {
					partial = true
					report.SkippedRecords++
					addLogExplanationWarning(report, "A malformed log record was skipped.")
					return false, 0
				}
			}
			report.ObservedLines++
			collector.observe(event.Data)
		case "gap":
			partial = true
			addLogExplanationWarning(report, "The log stream reported a retention gap.")
		case "error", "degraded":
			report.EndReason = event.Event
			if event.Event == "error" {
				problem := appLogsStreamErrorProblem(event.Data)
				report.ReadError = collector.message(problem.Title + ": " + problem.Detail)
				report.ReadErrorCode, report.ReadStatus = collector.message(problem.Code), problem.Status
			} else {
				report.ReadError = collector.message(appLogsDegradedMessage(event.Data))
			}
			return true, 3
		case "end":
			var end struct {
				Reason string `json:"reason"`
			}
			if !strings.HasPrefix(strings.TrimSpace(event.Data), "{") || json.Unmarshal([]byte(event.Data), &end) != nil {
				report.EndReason = "invalid_end"
				return true, 3
			}
			switch end.Reason {
			case "", "complete", "snapshot_complete", "archive_complete":
				report.EndReason, report.Complete = "end", !partial
			default:
				report.EndReason = "incomplete_end"
				addLogExplanationWarning(report, "Terminal reason: "+collector.message(end.Reason))
			}
			return true, 0
		case "status", "stage":
			// These describe deployment progress rather than customer log rows.
		default:
			if event.Data != "" {
				partial = true
				addLogExplanationWarning(report, "An unrecognized stream event was skipped.")
			}
		}
		return false, 0
	})
	if streamErr != nil {
		report.EndReason, report.ReadError, code = "read_error", collector.message(streamErr.Error()), 1
	}
	return code
}

func addLogExplanationWarning(report *deploymentLogExplanation, warning string) {
	if !slices.Contains(report.Warnings, warning) {
		report.Warnings = append(report.Warnings, warning)
	}
}
