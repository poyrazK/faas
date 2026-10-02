package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
)

type commitReadClient interface {
	GetCommitSource(context.Context, string) (api.CommitSourceResponse, error)
	GetCommitReceipt(context.Context, string, string) (api.CommitReceiptResponse, error)
	GetCommitOperation(context.Context, string) (api.CommitOperationResponse, error)
	ListCommitBlockedEvents(context.Context, string) (api.CommitBlockedEventsResponse, error)
}

type commitDoctorReport struct {
	SourceID string                       `json:"source_id"`
	Healthy  bool                         `json:"healthy"`
	Scope    string                       `json:"scope"`
	Checks   []commitwork.DiagnosticCheck `json:"checks"`
}

func cmdCommitDoctor(args []string) int {
	fs := newFlagSet("commit doctor", flag.ContinueOnError)
	file := fs.String("file", "", "optional local PostgreSQL credential file; read-only checks, never uploaded")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 1 {
		return 1
	}
	if _, err := uuid.Parse(positional[0]); err != nil {
		return printErr("Invalid source ID", err)
	}
	var raw string
	if *file != "" {
		var err error
		raw, err = readCommitConnectionFile(*file)
		if err != nil {
			return printErr("Cannot read connection file", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	source, err := client.GetCommitSource(ctx, positional[0])
	if err != nil {
		return printErr("Cannot inspect Commit source", err)
	}
	report := diagnoseCommitSource(source, time.Now().UTC())
	if *file != "" {
		report.Checks = append(report.Checks, commitwork.CheckDatabase(ctx, raw, source.ID)...)
	}
	report.Healthy = true
	for _, check := range report.Checks {
		if check.State != "pass" {
			report.Healthy = false
		}
	}
	if code := jsonOut(writeJSON(report)); code != 0 {
		return code
	}
	if !report.Healthy {
		return 1
	}
	return 0
}

func diagnoseCommitSource(source api.CommitSourceResponse, now time.Time) commitDoctorReport {
	report := commitDoctorReport{SourceID: source.ID, Scope: "Latest scheduler observation; optional database checks run locally and do not prove scheduler connectivity or production qualification."}
	enabled := commitwork.DiagnosticCheck{Name: "source_enabled", State: "pass", Code: "enabled"}
	if !source.Enabled {
		enabled.State, enabled.Code, enabled.Action = "fail", "paused", "Resume the source when its destination is ready."
	}
	policy := commitwork.DiagnosticCheck{Name: "operation_policy", State: "pass", Code: "bound"}
	if source.OperationPolicy == "" {
		policy.State, policy.Code, policy.Action = "fail", "legacy_source", "Create a source bound to an active account-scoped queue policy."
	}
	health := commitwork.DiagnosticCheck{Name: "scheduler_observation", State: "pass", Code: source.RelayStatus}
	if !commitObservationFresh(source.LastCheckedAt, now) {
		health.State, health.Code, health.Action = "unknown", "observation_stale", "Check that the scheduler relay is enabled and visiting this source; wait for a fresh observation."
	} else if source.RelayStatus != "healthy" {
		health.State, health.Action = "fail", commitRecoveryAction(source.RelayStatus)
	}
	report.Checks = []commitwork.DiagnosticCheck{enabled, policy, health}
	return report
}

func commitObservationFresh(checked *time.Time, now time.Time) bool {
	return checked != nil && !checked.After(now.Add(time.Minute)) && now.Sub(*checked) <= commitwork.ObservationMaxAge
}

func commitRecoveryAction(code string) string {
	switch code {
	case "credential_unavailable":
		return "Check scheduler age identities and register the source credential again."
	case "database_unavailable":
		return "Check scheduler DNS, approved destinations, TLS trust and the source credential."
	case "schema_unqualified":
		return "Install the supported customer schema or explicit upgrade as the database owner."
	case "source_binding_unqualified":
		return "Check the owner-controlled source binding; each database supports one source."
	case "blocked_events":
		return "Inspect blocked events, correct unaccepted rows and request replay."
	case "handoff_pending":
		return "Check destination readiness, Operations capacity and platform database health."
	case "healthy":
		return "Wait for the next relay pass; no event-specific acceptance has been observed."
	default:
		return "Inspect source health and scheduler recovery logs; preserve pending events."
	}
}

func readCommitConnectionFile(path string) (string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return "", err
	}
	if len(body) > 8192 {
		return "", errors.New("connection file exceeds 8192 bytes")
	}
	return strings.TrimSpace(string(body)), nil
}

type commitEventInspection struct {
	Source         api.CommitSourceResponse        `json:"source"`
	EventID        string                          `json:"event_id"`
	DatabaseCommit string                          `json:"database_commit"`
	Acceptance     string                          `json:"acceptance"`
	Execution      string                          `json:"execution"`
	Receipt        *api.CommitReceiptResponse      `json:"receipt,omitempty"`
	Operation      *api.CommitOperationResponse    `json:"operation,omitempty"`
	Blocked        *api.CommitBlockedEventResponse `json:"blocked,omitempty"`
	Observation    string                          `json:"observation"`
	Action         string                          `json:"action,omitempty"`
}

// Receipt absence says nothing about commit or rollback in the customer DB.
// Retained acceptance/completion facts take precedence over blocked snapshots.
func inspectCommitEvent(ctx context.Context, client commitReadClient, sourceID, event string, now time.Time) (commitEventInspection, error) {
	out := commitEventInspection{EventID: event, DatabaseCommit: "unknown", Acceptance: "unknown", Execution: "unknown", Observation: "No customer transaction outcome is inferred from receipt absence."}
	source, err := client.GetCommitSource(ctx, sourceID)
	if err != nil {
		return out, err
	}
	out.Source = source
	receipt, err := client.GetCommitReceipt(ctx, sourceID, event)
	if err == nil {
		out.Receipt, out.Acceptance = &receipt, "accepted"
		workID := receipt.OperationID
		if workID == "" {
			workID = receipt.InvocationID
		}
		operation, err := client.GetCommitOperation(ctx, workID)
		if err != nil {
			return out, err
		}
		out.Operation, out.Execution = &operation, operation.State
		out.Observation = "Durable Gregale acceptance and retained execution facts; acceptance alone does not prove the customer transaction outcome."
		return out, nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Status != http.StatusNotFound || apiErr.Problem.Code != "commit_event_not_found" {
		return out, err
	}
	blocked, err := client.ListCommitBlockedEvents(ctx, sourceID)
	if err != nil {
		return out, err
	}
	for _, item := range blocked.Items {
		if item.EventID == event {
			out.Blocked = &item
			out.DatabaseCommit = "observed"
			out.Observation = "The relay previously observed this committed row; the blocked snapshot may be stale."
			if commitObservationFresh(&item.ObservedAt, now) && commitObservationFresh(source.LastCheckedAt, now) && source.RelayStatus == "blocked_events" {
				out.Acceptance = "blocked"
				out.Action = "Correct the unaccepted source row and request replay with the same event ID."
				return out, nil
			}
		}
	}
	out.Action = commitRecoveryAction(source.RelayStatus)
	if !source.Enabled {
		out.Action = "The source is paused; resume it when the destination is ready."
	} else if !commitObservationFresh(source.LastCheckedAt, now) {
		out.Action = "Source observation is stale or absent; check scheduler relay health."
	}
	return out, nil
}

func cmdCommitInspect(verb string, args []string) int {
	fs := newFlagSet("commit "+verb, flag.ContinueOnError)
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait; timing out does not cancel durable work")
	interval := fs.Duration("interval", time.Second, "poll interval (100ms-1m)")
	until := fs.String("until", "completed", "wait for accepted or completed")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil || rejectUnexpectedFlagArgs(fs) || len(positional) != 2 {
		return 1
	}
	if *timeout <= 0 || *timeout > 24*time.Hour || *interval < 100*time.Millisecond || *interval > time.Minute || (*until != "accepted" && *until != "completed") {
		return 1
	}
	if verb != "wait" && fs.NFlag() != 0 {
		return 1
	}
	for _, id := range positional {
		if _, err := uuid.Parse(id); err != nil {
			return printErr("Invalid Commit identity", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, *timeout)
	defer stop()
	if verb == "inspect" {
		out, err := inspectCommitEvent(ctx, client, positional[0], positional[1], time.Now().UTC())
		if err != nil {
			return printErr("Cannot inspect Commit event", err)
		}
		return jsonOut(writeJSON(out))
	}
	out, err := waitCommitEvent(ctx, client, positional[0], positional[1], *until, *interval)
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if err != nil {
		return printErr("Stopped waiting; durable work has not been cancelled", err)
	}
	if out.Execution == "completed" || (*until == "accepted" && out.Acceptance == "accepted") {
		return 0
	}
	return 1
}

func waitCommitEvent(ctx context.Context, client commitReadClient, source, event, until string, interval time.Duration) (commitEventInspection, error) {
	var last commitEventInspection
	for {
		out, err := inspectCommitEvent(ctx, client, source, event, time.Now().UTC())
		if err != nil {
			return last, err
		}
		last = out
		if (until == "accepted" && out.Acceptance == "accepted") || out.Acceptance == "blocked" {
			return out, nil
		}
		switch out.Execution {
		case "completed", "cancelled", "failed", "expired":
			return out, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, fmt.Errorf("Commit wait interrupted: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
