package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const eventRecoverUsage = "usage: gregale events recover --source SOURCE --id ID --subscription SUB [--dry-run]"

type eventRecoveryResult struct {
	EventSource    string                          `json:"event_source"`
	EventID        string                          `json:"event_id"`
	SubscriptionID string                          `json:"subscription_id"`
	AppSlug        string                          `json:"app_slug,omitempty"`
	DryRun         bool                            `json:"dry_run"`
	Status         string                          `json:"status"`
	Reason         string                          `json:"reason,omitempty"`
	Action         *api.EventReceiptRecoveryAction `json:"action,omitempty"`
	Result         any                             `json:"result,omitempty"`
}

func cmdEventsRecover(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run")
	fs := newFlagSet("events recover", flag.ContinueOnError)
	source := fs.String("source", "", "published event source")
	id := fs.String("id", "", "published event id")
	subscription := fs.String("subscription", "", "captured recipient identifier")
	dryRun := fs.Bool("dry-run", false, "show the current recovery action without replaying")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*source) == "" || strings.TrimSpace(*id) == "" || strings.TrimSpace(*subscription) == "" {
		PrintUsage(os.Stderr, eventRecoverUsage, "events")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return recoverEventRecipient(context.Background(), client, strings.TrimSpace(*source), strings.TrimSpace(*id), strings.TrimSpace(*subscription), *dryRun)
}

func recoverEventRecipient(ctx context.Context, client *api.Client, source, id, subscription string, dryRun bool) int {
	recipient, err := findEventRecoveryRecipient(ctx, client, source, id, subscription)
	if err != nil {
		return printErr("Event recovery lookup failed", err)
	}
	out := eventRecoveryResult{EventSource: source, EventID: id, SubscriptionID: recipient.SubscriptionID, AppSlug: recipient.AppSlug, DryRun: dryRun, Status: "unavailable"}
	if len(recipient.RecoveryActions) == 0 {
		out.Reason = eventRecoveryUnavailableReason(recipient)
		return writeEventRecoveryResult(out)
	}
	action, targetID, err := selectEventRecoveryAction(source, id, recipient)
	if err != nil {
		return printErr("Event recovery action is unsupported", err)
	}
	out.Action, out.Status = &action, "available"
	if !dryRun {
		out.Result, err = executeEventRecovery(ctx, client, recipient.AppSlug, action, targetID)
		if err != nil {
			return printErr("Event recovery failed; inspect the receipt before retrying", err)
		}
		out.Status = "queued"
	}
	return writeEventRecoveryResult(out)
}

// Pages are bounded by the receipt API. Follow opaque cursors until the selected
// acceptance or backfill recipient is found; never replay siblings on the page.
func findEventRecoveryRecipient(ctx context.Context, client *api.Client, source, id, subscription string) (api.EventReceiptRecipientResponse, error) {
	seen := map[string]bool{"": true}
	after := ""
	for {
		receipt, err := client.GetEventReceipt(ctx, source, id, after, api.EventReceiptPageMax)
		if err != nil {
			return api.EventReceiptRecipientResponse{}, err
		}
		if receipt.EventSource != source || receipt.EventID != id {
			return api.EventReceiptRecipientResponse{}, fmt.Errorf("receipt identity does not match the requested event")
		}
		if !receipt.SnapshotCaptured {
			return api.EventReceiptRecipientResponse{}, fmt.Errorf("recipient snapshot is unavailable for this legacy event; use events deliveries to inspect retained work")
		}
		for _, recipient := range receipt.Recipients {
			if recipient.SubscriptionID == subscription {
				return recipient, nil
			}
		}
		if receipt.NextAfter == "" {
			return api.EventReceiptRecipientResponse{}, fmt.Errorf("subscription %q is not a retained recipient of this event", subscription)
		}
		if seen[receipt.NextAfter] {
			return api.EventReceiptRecipientResponse{}, fmt.Errorf("receipt pagination did not advance")
		}
		after = receipt.NextAfter
		seen[after] = true
	}
}

// Dispatch only the known replay endpoints. Do not forward credentials to an
// arbitrary URL supplied in a receipt, or infer a replay from execution state.
func selectEventRecoveryAction(source, id string, recipient api.EventReceiptRecipientResponse) (api.EventReceiptRecoveryAction, string, error) {
	if len(recipient.RecoveryActions) != 1 {
		return api.EventReceiptRecoveryAction{}, "", fmt.Errorf("expected exactly one recovery action")
	}
	action := recipient.RecoveryActions[0]
	if action.Method != http.MethodPost {
		return action, "", fmt.Errorf("unsupported recovery method %q", action.Method)
	}
	if recipient.WorkflowName != "" && action.Kind != "routing_replay" {
		return action, "", fmt.Errorf("workflow recipient recovery must target routing admission")
	}
	switch action.Kind {
	case "routing_replay":
		want := api.ReplayEventFanoutFailureRequest{EventSource: source, EventID: id, SubscriptionID: recipient.SubscriptionID}
		if !api.ValidAppSlug(recipient.AppSlug) || action.URL != "/v1/apps/"+url.PathEscape(recipient.AppSlug)+"/event-deliveries:replay-fanout-failure" || action.Body == nil || *action.Body != want {
			return action, "", fmt.Errorf("routing action does not match the selected recipient")
		}
		return action, "", nil
	case "handler_replay", "keyed_handler_replay":
		return selectEventHandlerRecovery(action, recipient)
	case "dead_letter_replay":
		prefix := "/v1/apps/" + url.PathEscape(recipient.AppSlug) + "/dlq/"
		target := strings.TrimSuffix(strings.TrimPrefix(action.URL, prefix), "/replay")
		if !api.ValidAppSlug(recipient.AppSlug) || !validEventRecoveryUUID(target) || action.URL != prefix+target+"/replay" || action.Body != nil {
			return action, "", fmt.Errorf("dead-letter action does not match the selected application")
		}
		return action, target, nil
	default:
		return action, "", fmt.Errorf("unsupported recovery kind %q; update gregale to use this action", action.Kind)
	}
}

func selectEventHandlerRecovery(action api.EventReceiptRecoveryAction, recipient api.EventReceiptRecipientResponse) (api.EventReceiptRecoveryAction, string, error) {
	execution := recipient.Execution
	if recipient.Recovery != nil && recipient.Recovery.LatestReplay != nil {
		execution = recipient.Recovery.LatestReplay
	}
	suffix := "/replay"
	if action.Kind == "keyed_handler_replay" {
		suffix = "/replay-keyed"
	}
	if execution == nil || !validEventRecoveryUUID(execution.InvocationID) || action.URL != "/v1/invocations/"+url.PathEscape(execution.InvocationID)+suffix || action.Body != nil {
		return action, "", fmt.Errorf("handler action does not match the selected execution")
	}
	return action, execution.InvocationID, nil
}

func validEventRecoveryUUID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil && !strings.ContainsAny(id, "/?#")
}

func executeEventRecovery(ctx context.Context, client *api.Client, slug string, action api.EventReceiptRecoveryAction, targetID string) (any, error) {
	switch action.Kind {
	case "routing_replay":
		return client.ReplayEventFanoutFailure(ctx, slug, *action.Body)
	case "handler_replay":
		return client.ReplayInvocation(ctx, targetID)
	case "keyed_handler_replay":
		return client.ReplayKeyedInvocation(ctx, targetID)
	case "dead_letter_replay":
		return client.ReplayDeadLetterEvent(ctx, slug, targetID)
	default:
		return nil, fmt.Errorf("unsupported recovery kind %q", action.Kind)
	}
}

func eventRecoveryUnavailableReason(recipient api.EventReceiptRecipientResponse) string {
	if recipient.WorkflowName != "" {
		switch recipient.Routing.State {
		case "pending", "processing":
			return "Workflow routing is still in progress; automatic recovery continues when the workflow runtime is enabled."
		case "filtered":
			return "The captured workflow trigger filtered out this event."
		case "enqueued":
			return "Workflow admission already completed; inspect the workflow run for step status and recovery."
		default:
			return "The receipt offers no workflow routing recovery action; inspect the event and workflow run."
		}
	}
	if recipient.AppSlug == "" {
		return "The recipient application is unavailable."
	}
	if recipient.Cancellation != nil || recipient.ExecutionUnavailable == "cancel_pending" {
		return "Pending delivery was cancelled; the receipt offers no replay."
	}
	execution := recipient.Execution
	if recipient.Recovery != nil && recipient.Recovery.LatestReplay != nil {
		execution = recipient.Recovery.LatestReplay
	}
	if execution != nil {
		switch execution.State {
		case "completed":
			return "This consumer has already completed successfully."
		case "pending", "dispatching":
			return "Consumer delivery or an existing replay is still in progress; automatic recovery continues."
		}
	}
	if recipient.ExecutionUnavailable != "" {
		return "Execution evidence is unavailable: " + recipient.ExecutionUnavailable + "."
	}
	if recipient.Routing.State == "pending" || recipient.Routing.State == "processing" {
		return "Routing is still in progress; automatic recovery continues."
	}
	if recipient.Routing.State == "filtered" {
		return "The captured subscription filtered out this event."
	}
	return "The receipt offers no recovery action; check retained records, deadlines, and replay eligibility with events inspect."
}

func writeEventRecoveryResult(out eventRecoveryResult) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(out)); code != 0 {
			return code
		}
	} else if out.Status == "unavailable" {
		_, _ = fmt.Fprintf(osStdout, "Recovery unavailable for %s: %s\n", oneLine(out.SubscriptionID), oneLine(out.Reason))
	} else if out.DryRun {
		_, _ = fmt.Fprintf(osStdout, "Would recover %s using %s: %s %s\n", oneLine(out.SubscriptionID), oneLine(out.Action.Kind), out.Action.Method, oneLine(out.Action.URL))
	} else {
		PrintOK(osStdout, "Recovery queued for %s using %s. Inspect the event to follow recipient progress.", oneLine(out.SubscriptionID), oneLine(out.Action.Kind))
	}
	if out.Status == "unavailable" && !out.DryRun {
		return 1
	}
	return 0
}
