package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationEventPlanItem struct {
	ID           string                         `json:"event_id"`
	Subscription string                         `json:"subscription_id"`
	Generation   *int64                         `json:"generation"`
	ReplayCount  int64                          `json:"replay_count"`
	Action       api.EventReceiptRecoveryAction `json:"action"`
}
type automationEventPlan struct {
	Version   int                       `json:"version"`
	App       string                    `json:"app"`
	AppID     string                    `json:"app_id"`
	Name      string                    `json:"name"`
	Revision  int64                     `json:"published_version"`
	Source    string                    `json:"event_source"`
	Key       string                    `json:"idempotency_key"`
	CreatedAt time.Time                 `json:"created_at"`
	Events    []automationEventPlanItem `json:"events"`
}
type automationEventPlanOutcome struct {
	ID         string `json:"event_id"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
	HTTPStatus int    `json:"http_status,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

func validateAutomationEventPlan(p automationEventPlan) error {
	if p.Version != 1 || !validAutomationRunSelection(p.App, p.Name) || !workflowUUIDPattern.MatchString(p.AppID) || p.Revision < 1 || strings.TrimSpace(p.Source) == "" || p.Key == "" || p.CreatedAt.IsZero() || len(p.Events) == 0 || len(p.Events) > api.AutomationRecoveryBatchMaxRuns {
		return errors.New("invalid event recovery plan scope or selection")
	}
	if err := validateDeployIdempotencyKey(p.Key); err != nil {
		return fmt.Errorf("validate event plan key: %w", err)
	}
	seen := map[string]bool{}
	for _, item := range p.Events {
		if strings.TrimSpace(item.ID) == "" || seen[item.ID] || item.Generation != nil && *item.Generation < 0 || item.ReplayCount < 0 || item.Subscription == "" {
			return errors.New("invalid or duplicate event selection")
		}
		seen[item.ID] = true
		recipient := api.EventReceiptRecipientResponse{AppSlug: p.App, WorkflowName: p.Name, SubscriptionID: item.Subscription, RecoveryActions: []api.EventReceiptRecoveryAction{item.Action}}
		if _, _, err := selectEventRecoveryAction(p.Source, item.ID, recipient); err != nil {
			return fmt.Errorf("validate pinned routing action: %w", err)
		}
	}
	return nil
}

// Inspect captured workflow recipients. ReplayCount identifies the reviewed
// replay cycle in legacy event routing; recipient routing also has Generation.
func inspectAutomationEvent(ctx context.Context, c *api.Client, p automationEventPlan, id string) (automationEventPlanItem, string, error) {
	after := ""
	seen := map[string]bool{"": true}
	var selected *api.EventReceiptRecipientResponse
	for page := 0; ; page++ {
		if page >= api.AutomationEventRecoveryReceiptMaxPages {
			return automationEventPlanItem{}, "receipt_scan_limit", nil
		}
		receipt, err := c.GetEventReceipt(ctx, p.Source, id, after, api.EventReceiptPageMax)
		if err != nil {
			return automationEventPlanItem{}, "receipt_unavailable", err
		}
		if receipt.EventSource != p.Source || receipt.EventID != id {
			return automationEventPlanItem{}, "receipt_identity_mismatch", nil
		}
		if receipt.RetainUntil != nil && !receipt.RetainUntil.After(time.Now()) {
			return automationEventPlanItem{}, "event_expired", nil
		}
		if !receipt.SnapshotCaptured {
			return automationEventPlanItem{}, "snapshot_unavailable", nil
		}
		for _, r := range receipt.Recipients {
			if r.AppID == p.AppID && r.WorkflowName == p.Name {
				if selected != nil {
					return automationEventPlanItem{}, "ambiguous_recipient", nil
				}
				copy := r
				selected = &copy
			}
		}
		if receipt.NextAfter == "" {
			break
		}
		if seen[receipt.NextAfter] {
			return automationEventPlanItem{}, "receipt_cursor_changed", nil
		}
		seen[receipt.NextAfter] = true
		after = receipt.NextAfter
	}
	if selected == nil {
		return automationEventPlanItem{}, "recipient_unavailable", nil
	}
	if selected.Origin == "backfill" {
		return automationEventPlanItem{}, "backfill_recovery_requires_separate_plan", nil
	}
	if selected.AppSlug != p.App {
		return automationEventPlanItem{}, "recipient_scope_changed", nil
	}
	if selected.Routing.DeliveryDeadlineAt != nil && !selected.Routing.DeliveryDeadlineAt.After(time.Now()) {
		return automationEventPlanItem{}, "delivery_deadline_expired", nil
	}
	if selected.Routing.State != "failed" || len(selected.RecoveryActions) != 1 {
		return automationEventPlanItem{}, "routing_action_unavailable", nil
	}
	action, _, err := selectEventRecoveryAction(p.Source, id, *selected)
	if err != nil {
		return automationEventPlanItem{}, "routing_action_unsupported", nil //nolint:nilerr // Unsupported actions are a preflight rejection, not a transport failure.
	}
	return automationEventPlanItem{ID: id, Subscription: selected.SubscriptionID, Generation: selected.Routing.Generation, ReplayCount: selected.Routing.ReplayCount, Action: action}, "", nil
}

func cmdAutomationsEventRecoveryPlan(args []string) int {
	fs := newFlagSet("automations event-recovery-plan", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	source := fs.String("source", "", "event source")
	key := fs.String("idempotency-key", "", "stable recovery batch key")
	out := fs.String("out", "", "new plan file")
	var ids []string
	seen := map[string]bool{}
	fs.Func("event", "retained event ID (repeatable)", func(id string) error {
		if strings.TrimSpace(id) == "" || seen[id] || len(ids) >= api.AutomationRecoveryBatchMaxRuns {
			return errors.New("invalid, duplicate or excessive event selection")
		}
		seen[id] = true
		ids = append(ids, id)
		return nil
	})
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if !validAutomationRunSelection(*app, *name) || strings.TrimSpace(*source) == "" || strings.TrimSpace(*key) == "" || *out == "" || len(ids) == 0 {
		return printErr("Invalid event plan arguments", errors.New("provide app, name, source, events, idempotency-key and out"))
	}
	if err := validateDeployIdempotencyKey(*key); err != nil {
		return printErr("Invalid batch key", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	appID, err := loadAutomationRunScope(ctx, c, *app, *name)
	if err != nil {
		return printErr("Could not resolve event plan scope", err)
	}
	a, err := c.GetAutomation(ctx, *app, *name)
	if err != nil {
		return printErr("Could not read automation revision", err)
	}
	p := automationEventPlan{Version: 1, App: *app, AppID: appID, Name: *name, Revision: a.PublishedVersion, Source: *source, Key: *key, CreatedAt: time.Now().UTC()}
	for _, id := range ids {
		item, reason, err := inspectAutomationEvent(ctx, c, p, id)
		if err != nil {
			return printErr("Could not inspect selected event", err)
		}
		if reason != "" {
			return printErr("Selected event unavailable", fmt.Errorf("%s: %s", id, reason))
		}
		p.Events = append(p.Events, item)
	}
	if err := validateAutomationEventPlan(p); err != nil {
		return printErr("Invalid event plan", err)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return printErr("Could not encode event plan", err)
	}
	if len(encoded)+1 > api.AutomationRecoveryPlanMaxBytes {
		return printErr("Event plan too large", errors.New("event plan exceeds 64 KiB"))
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return printErr("Could not create event plan", err)
	}
	defer func() { _ = f.Close() }()
	if err := persistRecoveryRecord(f, p); err != nil {
		return printErr("Could not persist event plan", err)
	}
	if err := f.Close(); err != nil {
		return printErr("Could not close event plan", err)
	}
	return 0
}

func readAutomationEventPlan(path string) (automationEventPlan, error) {
	var p automationEventPlan
	f, err := openCustomerFile(path)
	if err != nil {
		return p, fmt.Errorf("open event plan: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return p, fmt.Errorf("inspect event plan: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > api.AutomationRecoveryPlanMaxBytes {
		return p, errors.New("event plan must be a regular file of at most 64 KiB")
	}
	d := json.NewDecoder(io.LimitReader(f, api.AutomationRecoveryPlanMaxBytes+1))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, fmt.Errorf("decode event plan: %w", err)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return p, errors.New("event plan must contain exactly one JSON object")
	}
	return p, validateAutomationEventPlan(p)
}

func recoverAutomationEvent(ctx context.Context, c *api.Client, p automationEventPlan, item automationEventPlanItem) automationEventPlanOutcome {
	out := automationEventPlanOutcome{ID: item.ID, Outcome: "changed"}
	a, err := c.GetAutomation(ctx, p.App, p.Name)
	if err != nil {
		return eventPlanAPIFailure(out, "automation_read_failed", err)
	}
	if a.Name != p.Name || a.PublishedVersion != p.Revision {
		out.Reason = "automation_revision_changed"
		return out
	}
	current, reason, err := inspectAutomationEvent(ctx, c, p, item.ID)
	if err != nil {
		return eventPlanAPIFailure(out, reason, err)
	}
	if reason != "" {
		out.Outcome = "blocked"
		out.Reason = reason
		return out
	}
	if !reflect.DeepEqual(current, item) {
		out.Reason = "routing_state_changed"
		return out
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return eventPlanAPIFailure(out, "plan_encode_failed", err)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%q:%q:%q:%q:%q", p.Key, p.AppID, p.Name, p.Source, string(encoded))))
	requestCtx := api.ContextWithIdempotencyKey(ctx, fmt.Sprintf("automation-event-recover-%x", digest))
	request := *item.Action.Body
	request.RecoveryExpectation = &api.EventRoutingRecoveryExpectation{WorkflowName: p.Name, PublishedVersion: p.Revision, ReplayCount: item.ReplayCount, Generation: item.Generation}
	response, err := c.ReplayEventFanoutFailure(requestCtx, p.App, request)
	if err != nil {
		return eventPlanAPIFailure(out, "replay_unconfirmed", err)
	}
	if response.EventID != item.ID || response.EventSource != p.Source || response.SubscriptionID != item.Subscription || response.State != "pending" {
		out.Outcome = "failed"
		out.Reason = "replay_response_unconfirmed"
		return out
	}
	out.Outcome = "queued"
	out.Reason = "routing_replay_accepted"
	return out
}
func eventPlanAPIFailure(out automationEventPlanOutcome, reason string, err error) automationEventPlanOutcome {
	out.Outcome = "failed"
	out.Reason = reason
	var remote *api.APIError
	if errors.As(err, &remote) {
		if remote.Problem.Code == api.CodeEventRecoveryChanged {
			out.Outcome = "changed"
			out.Reason = "routing_state_changed"
			out.HTTPStatus = remote.Problem.Status
			out.ErrorCode = remote.Problem.Code
			return out
		}
		out.HTTPStatus = remote.Problem.Status
		out.ErrorCode = remote.Problem.Code
		if out.HTTPStatus == 404 || out.HTTPStatus == 410 {
			out.Outcome = "blocked"
			out.Reason = "event_or_recipient_unavailable"
		} else if out.HTTPStatus >= 400 && out.HTTPStatus < 500 {
			out.Outcome = "blocked"
			out.Reason = "replay_rejected"
		}
	}
	return out
}
func cmdAutomationsEventRecover(args []string) int {
	fs := newFlagSet("automations event-recover", flag.ContinueOnError)
	path := fs.String("plan", "", "reviewed event plan")
	journalPath := fs.String("journal", "", "new execution journal")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *path == "" || *journalPath == "" {
		return printErr("Invalid arguments", errors.New("provide plan and journal"))
	}
	p, err := readAutomationEventPlan(*path)
	if err != nil {
		return printErr("Invalid event recovery plan", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	journal, err := os.OpenFile(*journalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return printErr("Could not create event journal", err)
	}
	defer func() { _ = journal.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	appID, err := loadAutomationRunScope(ctx, c, p.App, p.Name)
	if err != nil {
		return printErr("Could not resolve plan scope", err)
	}
	if appID != p.AppID {
		return printErr("Event plan scope changed", errors.New("app identity changed"))
	}
	if err := persistRecoveryRecord(journal, map[string]any{"kind": "started", "at": time.Now().UTC(), "plan": p}); err != nil {
		return printErr("Could not persist event journal", err)
	}
	outcomes := []automationEventPlanOutcome{}
	for _, item := range p.Events {
		if ctx.Err() != nil {
			return printErr("Event recovery interrupted", ctx.Err())
		}
		if err := persistRecoveryRecord(journal, map[string]any{"kind": "attempted", "at": time.Now().UTC(), "event": item}); err != nil {
			return printErr("Event recovery stopped before attempt", err)
		}
		result := recoverAutomationEvent(ctx, c, p, item)
		outcomes = append(outcomes, result)
		if err := persistRecoveryRecord(journal, map[string]any{"kind": "outcome", "at": time.Now().UTC(), "outcome": result}); err != nil {
			return printErr("Event recovery stopped; inspect journal before retry", err)
		}
	}
	if err := persistRecoveryRecord(journal, map[string]any{"kind": "completed", "at": time.Now().UTC()}); err != nil {
		return printErr("Could not persist completion", err)
	}
	if err := journal.Close(); err != nil {
		return printErr("Could not close event journal", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(outcomes)); code != 0 {
			return code
		}
	} else {
		for _, out := range outcomes {
			if _, err := fmt.Fprintf(osStdout, "%s: %s (%s)\n", oneLine(out.ID), out.Outcome, out.Reason); err != nil {
				return printErr("Could not write event outcomes", err)
			}
		}
	}
	for _, out := range outcomes {
		if out.Outcome != "queued" {
			return 1
		}
	}
	return 0
}
