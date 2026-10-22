package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const lineageControlPlanFormat = "gregale.event-lineage-control-plan"

type lineageControlPlanSource struct {
	Attention  string    `json:"attention"`
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
	NextAfter  string    `json:"next_after"`
}
type lineageControlPlanEntry struct {
	JobRequestID      string    `json:"job_request_id"`
	ExpectedCreatedAt time.Time `json:"expected_created_at"`
	RetainUntil       time.Time `json:"retain_until"`
	Action            string    `json:"action"`
	CommandID         string    `json:"command_id"`
}
type lineageControlPlan struct {
	Format         string                    `json:"format"`
	Version        int                       `json:"version"`
	PlanID         string                    `json:"plan_id"`
	AppID          string                    `json:"app_id"`
	SubscriptionID string                    `json:"subscription_id"`
	Source         lineageControlPlanSource  `json:"source"`
	Actions        []lineageControlPlanEntry `json:"actions"`
}

func normalizeLineageControlPlan(plan *lineageControlPlan) error {
	if plan.Format != lineageControlPlanFormat || plan.Version != 1 {
		return fmt.Errorf("unsupported control-plan format/version")
	}
	for _, id := range []*string{&plan.PlanID, &plan.AppID, &plan.SubscriptionID} {
		parsed, err := uuid.Parse(*id)
		if err != nil || parsed == uuid.Nil {
			return fmt.Errorf("plan scope and identity must be nonzero UUIDs")
		}
		*id = parsed.String()
	}
	source := api.EventExecutionLineageRetryJobAttentionOptions{Attention: plan.Source.Attention}
	if source.Attention == "" || source.Validate(plan.Source.Status) != nil || plan.Source.ObservedAt.IsZero() || len(plan.Source.NextAfter) > api.EventExecutionLineageRetryJobsCursorMaxBytes {
		return fmt.Errorf("invalid control-plan source metadata")
	}
	if len(plan.Actions) < 1 || len(plan.Actions) > api.EventExecutionLineageControlPlanMaxJobs {
		return fmt.Errorf("plan must contain 1..%d distinct jobs", api.EventExecutionLineageControlPlanMaxJobs)
	}
	jobs, commands := make(map[string]bool), make(map[string]bool)
	for i := range plan.Actions {
		entry := &plan.Actions[i]
		job, jobErr := uuid.Parse(entry.JobRequestID)
		command, commandErr := uuid.Parse(entry.CommandID)
		if jobErr != nil || commandErr != nil || job == uuid.Nil || command == uuid.Nil || jobs[job.String()] || commands[command.String()] || entry.ExpectedCreatedAt.IsZero() || !entry.RetainUntil.After(entry.ExpectedCreatedAt) || (entry.Action != "pause" && entry.Action != "resume" && entry.Action != "cancel") {
			return fmt.Errorf("invalid or duplicate job/command at plan entry %d", i+1)
		}
		entry.JobRequestID, entry.CommandID = job.String(), command.String()
		entry.ExpectedCreatedAt, entry.RetainUntil = entry.ExpectedCreatedAt.UTC(), entry.RetainUntil.UTC()
		jobs[entry.JobRequestID], commands[entry.CommandID] = true, true
	}
	return nil
}
func readLineageControlPlan(path string) (lineageControlPlan, error) {
	var plan lineageControlPlan
	info, err := os.Stat(path)
	if err != nil {
		return plan, err
	}
	if !info.Mode().IsRegular() || info.Size() > api.EventExecutionLineageControlPlanMaxBytes {
		return plan, fmt.Errorf("plan must be a regular file of at most %d bytes", api.EventExecutionLineageControlPlanMaxBytes)
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return plan, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.EventExecutionLineageControlPlanMaxBytes+1))
	if err != nil {
		return plan, err
	}
	if len(raw) > api.EventExecutionLineageControlPlanMaxBytes {
		return plan, fmt.Errorf("control plan exceeds byte limit")
	}
	if err = rejectDuplicateLineageJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return plan, err
	}
	fields, err := lineageHistoryObjectFields(raw, []string{"format", "version", "plan_id", "app_id", "subscription_id", "source", "actions"}, nil)
	if err != nil {
		return plan, err
	}
	if _, err = lineageHistoryObjectFields(fields["source"], []string{"attention", "status", "observed_at", "next_after"}, nil); err != nil {
		return plan, err
	}
	var actions []json.RawMessage
	if err = json.Unmarshal(fields["actions"], &actions); err != nil {
		return plan, err
	}
	for _, entry := range actions {
		if _, err = lineageHistoryObjectFields(entry, []string{"job_request_id", "expected_created_at", "retain_until", "action", "command_id"}, nil); err != nil {
			return plan, err
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&plan); err != nil {
		return plan, err
	}
	if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return plan, fmt.Errorf("plan must contain one JSON object")
	}
	return plan, normalizeLineageControlPlan(&plan)
}
func buildLineageControlPlan(page api.EventExecutionLineageRetryJobs, sub, status, attention, action string) (lineageControlPlan, error) {
	plan := lineageControlPlan{Format: lineageControlPlanFormat, Version: 1, PlanID: uuid.NewString(), SubscriptionID: sub, Actions: make([]lineageControlPlanEntry, 0, len(page.Jobs))}
	if len(page.Jobs) == 0 {
		return plan, fmt.Errorf("attention page has no matching jobs")
	}
	if page.Attention == nil || page.Attention.Filter != attention || page.Attention.Coverage != "live_matching_retry_jobs_page" || page.Attention.HasMore != (page.NextAfter != "") {
		return plan, fmt.Errorf("compatible attention metadata is required")
	}
	plan.Source = lineageControlPlanSource{Attention: attention, Status: status, ObservedAt: page.Attention.ObservedAt, NextAfter: page.NextAfter}
	for _, job := range page.Jobs {
		app, err := uuid.Parse(job.AppID)
		if err != nil || app == uuid.Nil || (plan.AppID != "" && plan.AppID != app.String()) || !sameBindingDeployment(job.SubscriptionID, sub) || job.Attention == nil {
			return plan, fmt.Errorf("invalid attention-list scope")
		}
		plan.AppID = app.String()
		detail := job.Attention
		if (job.Status != "pending" && job.Status != "paused") || (status != "" && job.Status != status) || !sameBindingDeployment(detail.RequestID, job.RequestID) || detail.Status != job.Status || !detail.CreatedAt.Equal(job.CreatedAt) || !detail.RetainUntil.Equal(job.RetainUntil) || detail.Processed != job.Processed || detail.Selected != job.Selected || job.Selected < 1 || job.Selected > api.EventExecutionLineageRetryJobMaxCases || job.Processed < 0 || job.Processed >= job.Selected || ((attention != "any" || (!detail.StalledPending && !detail.RetentionRisk)) && (attention != "stalled" || !detail.StalledPending) && (attention != "retention_risk" || !detail.RetentionRisk)) {
			return plan, fmt.Errorf("invalid attention job observation")
		}
		plan.Actions = append(plan.Actions, lineageControlPlanEntry{JobRequestID: job.RequestID, ExpectedCreatedAt: job.CreatedAt, RetainUntil: job.RetainUntil, Action: action, CommandID: uuid.NewString()})
	}
	return plan, normalizeLineageControlPlan(&plan)
}
