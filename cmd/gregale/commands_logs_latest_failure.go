package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A failed deployment may have no typed error code. Do not skip those rows or
// fall back to a successful deployment when history is unavailable/incomplete.
func selectLatestLogFailure(ctx context.Context, client *Client, slug string) (*api.DeploymentResponse, error) {
	before := ""
	seen := map[string]bool{}
	for range 50 {
		page, err := client.ListAppDeployments(ctx, slug, before, 10)
		if err != nil {
			return nil, fmt.Errorf("read deployment history: %w", err)
		}
		for _, dep := range page.Items {
			if dep.Status == deploymentStatusFailed {
				if dep.ID == "" {
					return nil, errors.New("failed deployment has no ID")
				}
				return &dep, nil
			}
		}
		if page.NextBefore == "" {
			return nil, nil
		}
		if seen[page.NextBefore] {
			return nil, errors.New("deployment history repeated a cursor; latest failure could not be established")
		}
		seen[page.NextBefore] = true
		before = page.NextBefore
	}
	return nil, errors.New("deployment history search reached 50 pages; use inspect --errors or an explicit deployment ID")
}

type failureLogReceipt struct {
	Type        string `json:"type"`
	App         string `json:"app_slug"`
	Profile     string `json:"profile"`
	Scope       string `json:"scope"`
	Deployment  string `json:"deployment_id,omitempty"`
	Source      string `json:"source"`
	CreatedAt   string `json:"deployment_created_at,omitempty"`
	Limit       int    `json:"entry_limit"`
	Lines       int    `json:"lines"`
	Message     string `json:"message,omitempty"`
	NextCommand string `json:"next_command,omitempty"`
}

func runLatestFailureLogs(slug string) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	dep, err := selectLatestLogFailure(ctx, client, slug)
	if err != nil {
		return failureLogReadError(ctx, "Could not select latest failure", err)
	}
	receipt := failureLogReceipt{Type: "selection", App: slug, Profile: currentProfile(), Scope: "app deployment history (not filtered by environment)", Source: "build/deploy", Limit: 500}
	if dep == nil {
		receipt.Type = "summary"
		receipt.Message = "No failed deployment was found in the available app deployment history. Runtime or request failures are not covered by this selector."
		receipt.NextCommand = recoveryCLI() + " inspect " + recoveryArgument(slug)
		if jsonOutput {
			if code := jsonOut(json.NewEncoder(osStdout).Encode(receipt)); code != 0 {
				return code
			}
		} else {
			_, _ = fmt.Fprintf(osStdout, "%s\nNext step (POSIX shells):\n  %s\n", receipt.Message, receipt.NextCommand)
		}
		return 1
	}
	receipt.Deployment, receipt.CreatedAt = dep.ID, dep.CreatedAt
	receipt.NextCommand = recoveryCLI() + " deployment get " + recoveryArgument(dep.ID)
	if jsonOutput {
		if code := jsonOut(json.NewEncoder(osStdout).Encode(receipt)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Failure logs: app=%s; profile=%s\nDeployment: %s\nScope: %s\nSource: build/deploy; most recent %d retained entries\n", oneLine(slug), oneLine(receipt.Profile), oneLine(dep.ID), receipt.Scope, receipt.Limit)
		if dep.CreatedAt != "" {
			_, _ = fmt.Fprintf(osStdout, "Deployment created: %s (not the failure timestamp)\n", oneLine(dep.CreatedAt))
		}
	}
	body, err := client.StreamDeploymentLogs(ctx, dep.ID, nil, receipt.Limit, false)
	if err != nil {
		return failureLogReadError(ctx, "Failure logs unavailable", err)
	}
	defer func() { _ = body.Close() }()
	decoder := api.NewDecoder(body)
	decoder.SetCloseFn(body.Close)
	defer func() { _ = decoder.Close() }()
	var outputErr error
	code, streamErr := consumeLogStream(ctx, decoder.Events(), decoder.Errors(), func(event api.Event) (bool, int) {
		switch event.Event {
		case "end":
			return true, 0
		case "error", "degraded":
			outputErr = errors.New("the server reported unavailable failure logs; inspect the exact deployment for persisted failure evidence")
			return true, 3
		case "log":
			var entry struct {
				Line string `json:"line"`
			}
			if err := json.Unmarshal([]byte(event.Data), &entry); err != nil {
				outputErr = errors.New("server returned an invalid deployment log entry")
				return true, 3
			}
			receipt.Lines++
			if jsonOutput {
				outputErr = json.NewEncoder(osStdout).Encode(struct {
					Type       string `json:"type"`
					Deployment string `json:"deployment_id"`
					Line       string `json:"line"`
				}{"log", dep.ID, entry.Line})
			} else {
				_, outputErr = fmt.Fprintln(osStdout, terminalSafe(entry.Line))
			}
			if outputErr != nil {
				return true, 1
			}
		}
		return false, 0
	})
	if outputErr != nil {
		return printErr("Failure log stream failed", outputErr)
	}
	if streamErr != nil {
		return failureLogReadError(ctx, "Failure log stream closed", streamErr)
	}
	if code != 0 {
		if ctx.Err() == context.DeadlineExceeded {
			return 124
		}
		return code
	}
	receipt.Type = "summary"
	if receipt.Lines == 0 {
		receipt.Message = "No retained build/deploy log entries were returned. The response does not establish whether logs expired or were never recorded. Inspect persisted failure evidence."
	} else {
		receipt.Message = "Read completed for the selected deployment; runtime and HTTP logs are separate."
	}
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "%s\nNext step (POSIX shells):\n  %s\n", receipt.Message, receipt.NextCommand)
	return 0
}

func failureLogReadError(ctx context.Context, title string, err error) int {
	code := printErr(title, err)
	if ctx.Err() == context.DeadlineExceeded {
		return 124
	}
	if ctx.Err() == context.Canceled {
		return 130
	}
	return code
}
