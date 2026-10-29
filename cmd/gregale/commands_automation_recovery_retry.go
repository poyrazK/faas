package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationRecoveryRetryRow struct {
	Selectable          bool     `json:"selectable"`
	Status              string   `json:"status"`
	CapacityScope       string   `json:"capacity_scope,omitempty"`
	Position            int64    `json:"position"`
	RunID               string   `json:"run_id"`
	ExpectedResumeCount int      `json:"expected_resume_count"`
	SavedOutcome        string   `json:"saved_outcome,omitempty"`
	Eligible            bool     `json:"eligible"`
	Reason              string   `json:"reason,omitempty"`
	Blockers            []string `json:"blockers,omitempty"`
}
type automationRecoveryRetryPreview struct {
	App         string                       `json:"app"`
	AppID       string                       `json:"app_id"`
	Name        string                       `json:"name"`
	ParentJobID string                       `json:"parent_job_id"`
	ObservedAt  time.Time                    `json:"observed_at"`
	Rows        []automationRecoveryRetryRow `json:"items"`
	PlanPath    string                       `json:"plan_path,omitempty"`
}

func cmdAutomationsRecoveryRetryPlan(args []string) int {
	fs := newFlagSet("automations recovery-retry-plan", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	job := fs.String("job", "", "parent recovery job")
	path := fs.String("plan-out", "", "new reviewed retry plan file")
	key := fs.String("key", "", "stable retry plan key")
	var positions multiFlag
	fs.Var(&positions, "position", "reviewed parent item position (repeatable)")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" || !workflowUUIDPattern.MatchString(*job) || *path != "" && (len(positions) == 0 || *key == "") || *path == "" && (len(positions) != 0 || *key != "") {
		return printErr("Invalid retry plan arguments", errors.New("provide --app and --job; saving also requires --position, --key and --plan-out"))
	}
	if *key != "" {
		if err := validateDeployIdempotencyKey(*key); err != nil {
			return printErr("Invalid plan key", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := loadAutomationRecoveryRetryPreview(ctx, c, *app, *job)
	if err != nil {
		return printErr("Could not review workflow recovery failures", err)
	}
	if *path != "" {
		p, err := automationRecoveryRetryPlan(out, positions, *key)
		if err != nil {
			return printErr("Retry selection is not eligible", err)
		}
		if err = writeAutomationRecoveryPlanFile(*path, p); err != nil {
			return printErr("Could not save retry plan", err)
		}
		out.PlanPath = *path
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Workflow recovery retry: %s / %s | parent %s\nPOSITION\tRUN\tREVIEWED COUNT\tSAVED OUTCOME\tSELECTABLE\tSTATUS\tREASON\n", oneLine(out.App), oneLine(out.Name), oneLine(out.ParentJobID))
	for _, row := range out.Rows {
		_, _ = fmt.Fprintf(osStdout, "%d\t%s\t%d\t%s\t%t\t%s\t%s\n", row.Position, oneLine(row.RunID), row.ExpectedResumeCount, oneLine(row.SavedOutcome), row.Selectable, oneLine(row.Status), oneLine(row.Reason))
	}
	if out.PlanPath != "" {
		_, _ = fmt.Fprintf(osStdout, "Saved reviewed retry plan: %s\nSubmit: gregale automation recovery create --plan %s\n", oneLine(out.PlanPath), oneLine(out.PlanPath))
	}
	return 0
}

func loadAutomationRecoveryRetryPreview(ctx context.Context, c *api.Client, app, parent string) (automationRecoveryRetryPreview, error) {
	out := automationRecoveryRetryPreview{App: app, ParentJobID: parent, Rows: []automationRecoveryRetryRow{}}
	owner, err := c.GetApp(ctx, app)
	if err != nil {
		return out, fmt.Errorf("resolve retry app: %w", err)
	}
	review, err := c.GetWorkflowRecoveryRetryPreflight(ctx, parent)
	if err != nil {
		return out, fmt.Errorf("review saved workflow failures: %w", err)
	}
	if review.JobID != parent || review.AppID != owner.ID || review.ReviewMode != "workflow_retry" || !validAutomationRunSelection(app, review.WorkflowName) || review.ObservedAt.IsZero() || review.ReviewedCount < 1 || review.ReviewedCount > api.AutomationRecoveryBatchMaxRuns || int64(len(review.Sample)) != review.ReviewedCount {
		return out, errors.New("retry preflight scope or selection does not match")
	}
	out.AppID, out.Name, out.ObservedAt = owner.ID, review.WorkflowName, review.ObservedAt
	after := int64(0)
	for _, item := range review.Sample {
		if item.Position <= after || item.Position > review.ReviewedCount || !workflowUUIDPattern.MatchString(item.WorkflowRunID) || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount < 1 || *item.ExpectedResumeCount > api.WorkflowRunMaxResumes+1 {
			return out, errors.New("invalid retry preflight identity")
		}
		eligible := item.Status == "eligible" && item.Reason == "eligible" || item.Status == "waiting" && item.Reason == "capacity"
		if eligible && item.SavedOutcome != "failed" {
			return out, errors.New("retry preflight lacks a saved failure")
		}
		row := automationRecoveryRetryRow{Position: item.Position, RunID: item.WorkflowRunID, ExpectedResumeCount: *item.ExpectedResumeCount, SavedOutcome: item.SavedOutcome, Eligible: item.Status == "eligible" && item.Reason == "eligible", Selectable: eligible, Status: item.Status, Reason: item.Reason, CapacityScope: item.CapacityScope, Blockers: item.Blockers}
		out.Rows = append(out.Rows, row)
		after = item.Position
	}
	return out, nil
}

func automationRecoveryRetryPlan(preview automationRecoveryRetryPreview, positions []string, key string) (automationRecoveryPlan, error) {
	p := automationRecoveryPlan{Version: 1, App: preview.App, AppID: preview.AppID, Name: preview.Name, ParentJobID: preview.ParentJobID, Key: key, CreatedAt: time.Now().UTC(), Runs: []automationRecoverySelection{}}
	seen := map[int64]bool{}
	for _, raw := range positions {
		position, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || position < 1 || seen[position] {
			return p, errors.New("positions must be unique positive integers")
		}
		seen[position] = true
		found := false
		for _, row := range preview.Rows {
			if row.Position != position {
				continue
			}
			if !row.Selectable && !row.Eligible {
				return p, fmt.Errorf("parent item %d is not eligible (%s)", position, row.Reason)
			}
			p.Runs = append(p.Runs, automationRecoverySelection{ID: row.RunID, ExpectedResumeCount: row.ExpectedResumeCount, ParentPosition: row.Position})
			found = true
			break
		}
		if !found {
			return p, fmt.Errorf("parent item %d is not retained", position)
		}
	}
	return p, validateAutomationRecoveryPlan(p)
}
