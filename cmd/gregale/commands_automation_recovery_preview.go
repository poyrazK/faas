package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationRecoveryRun struct {
	ID         string                    `json:"id"`
	Status     string                    `json:"status"`
	ObservedAt string                    `json:"observed_at"`
	Resume     api.WorkflowResumePreview `json:"resume"`
}
type automationRecoveryRunPage struct {
	Status     string                  `json:"status"`
	Total      int                     `json:"total"`
	Offset     int                     `json:"offset"`
	NextOffset *int                    `json:"next_offset,omitempty"`
	Runs       []automationRecoveryRun `json:"runs"`
}
type automationRecoveryEventDecision struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`
}
type automationRecoveryPreview struct {
	EventDecisions    []automationRecoveryEventDecision       `json:"event_decisions,omitempty"`
	App               string                                  `json:"app"`
	Name              string                                  `json:"name"`
	StartedAt         time.Time                               `json:"started_at"`
	CompletedAt       time.Time                               `json:"completed_at"`
	From              time.Time                               `json:"from"`
	Until             time.Time                               `json:"until"`
	Limit             int                                     `json:"limit"`
	AutomationVersion int64                                   `json:"automation_version"`
	Enabled           bool                                    `json:"enabled"`
	FailurePaused     bool                                    `json:"failure_paused"`
	FailureGeneration int64                                   `json:"failure_generation"`
	RetainedEvents    int64                                   `json:"retained_events"`
	Runs              []automationRecoveryRunPage             `json:"runs"`
	Events            *api.WorkflowEventReplayPreviewResponse `json:"events,omitempty"`
	Schedule          *api.WorkflowSchedulePreviewResponse    `json:"schedule,omitempty"`
	TriggerReason     string                                  `json:"trigger_reason"`
}

func cmdAutomationsRecoveryPreview(args []string) int {
	fs := newFlagSet("automations recovery-preview", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	from := fs.String("from", "", "RFC3339 start for retained events and schedule preview")
	until := fs.String("until", "", "RFC3339 end/evaluation time (default now)")
	limit := fs.Int("limit", api.AutomationRecoveryPreviewDefaultLimit, "maximum runs per status and event scan page")
	offset := fs.Int("run-offset", 0, "offset in each failed/dead run page")
	after := fs.String("event-after", "", "retained event preview cursor")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	start, err := time.Parse(time.RFC3339Nano, *from)
	if err != nil {
		return printErr("Invalid recovery range", errors.New("--from requires an RFC3339 timestamp"))
	}
	end := time.Now().UTC()
	if *until != "" {
		end, err = time.Parse(time.RFC3339Nano, *until)
		if err != nil {
			return printErr("Invalid recovery range", errors.New("--until requires an RFC3339 timestamp"))
		}
	}
	options := api.WorkflowEventReplayPreviewOptions{From: start.UTC(), Until: end.UTC(), Limit: *limit, After: *after}
	if !validAutomationRunSelection(*app, *name) || *limit < 1 || *limit > api.AutomationRecoveryPreviewMaxLimit || *offset < 0 {
		return printErr("Invalid recovery selection", fmt.Errorf("provide --app, --name, --limit 1..%d and a nonnegative --run-offset", api.AutomationRecoveryPreviewMaxLimit))
	}
	if err := options.Validate(); err != nil {
		return printErr("Invalid recovery range", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := loadAutomationRecoveryPreview(ctx, client, *app, *name, *offset, options)
	if err != nil {
		return printErr("Could not preview automation recovery", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if err := renderAutomationRecoveryPreview(osStdout, out); err != nil {
		return printErr("Could not write recovery preview", err)
	}
	return 0
}

func loadAutomationRecoveryPreview(ctx context.Context, c *api.Client, app, name string, offset int, options api.WorkflowEventReplayPreviewOptions) (automationRecoveryPreview, error) {
	out := automationRecoveryPreview{App: app, Name: name, StartedAt: time.Now().UTC(), From: options.From.UTC(), Until: options.Until.UTC(), Limit: options.Limit, Runs: []automationRecoveryRunPage{}}
	application, err := c.GetApp(ctx, app)
	if err != nil {
		return out, err
	}
	automation, err := c.GetAutomation(ctx, app, name)
	if err != nil {
		return out, err
	}
	if application.ID == "" || automation.Name != name {
		return out, errors.New("automation lookup scope mismatch")
	}
	policy, err := c.GetAutomationFailurePolicy(ctx, app, name)
	if err != nil {
		return out, err
	}
	out.AutomationVersion, out.Enabled, out.FailurePaused, out.FailureGeneration, out.RetainedEvents = automation.Version, automation.Enabled, policy.Paused, policy.Generation, policy.RetainedEvents
	for _, status := range []string{"failed", "dead"} {
		page, err := loadAutomationRecoveryRuns(ctx, c, app, application.ID, name, status, offset, options.Limit)
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, page)
	}
	if err := loadAutomationRecoveryTrigger(ctx, c, automation, options, &out); err != nil {
		return out, err
	}
	out.CompletedAt = time.Now().UTC()
	return out, nil
}

func loadAutomationRecoveryRuns(ctx context.Context, c *api.Client, app, appID, name, status string, offset, limit int) (automationRecoveryRunPage, error) {
	page := automationRecoveryRunPage{Status: status, Offset: offset, Runs: []automationRecoveryRun{}}
	listed, err := c.ListWorkflowRunsWithOptions(ctx, app, api.WorkflowRunListOptions{WorkflowName: name, Status: status, Limit: limit, Offset: offset})
	if err != nil {
		return page, err
	}
	if len(listed.Runs) > limit {
		return page, errors.New("run history exceeded the requested page limit")
	}
	page.Total = listed.Total
	for _, run := range listed.Runs {
		if run.AppID != appID || run.WorkflowName != name || run.Status != status || !workflowUUIDPattern.MatchString(strings.ToLower(run.ID)) {
			return page, errors.New("run history scope mismatch")
		}
		diagnostics, err := c.GetWorkflowRunDiagnostics(ctx, run.ID)
		if err != nil {
			return page, err
		}
		if !strings.EqualFold(diagnostics.RunID, run.ID) || diagnostics.WorkflowName != name {
			return page, errors.New("run diagnostics scope mismatch")
		}
		page.Runs = append(page.Runs, automationRecoveryRun{ID: run.ID, Status: diagnostics.Status, ObservedAt: diagnostics.ObservedAt, Resume: diagnostics.Resume})
	}
	if len(listed.Runs) > 0 && offset+len(listed.Runs) < listed.Total {
		next := offset + len(listed.Runs)
		page.NextOffset = &next
	}
	return page, nil
}

func loadAutomationRecoveryTrigger(ctx context.Context, c *api.Client, automation api.AutomationResponse, options api.WorkflowEventReplayPreviewOptions, out *automationRecoveryPreview) error {
	if automation.Published == nil || automation.Published.Trigger == nil {
		out.TriggerReason = "no_published_trigger"
		return nil
	}
	switch automation.Published.Trigger.Type {
	case "event":
		result, err := c.PreviewWorkflowEventReplay(ctx, out.App, out.Name, options)
		if err != nil {
			return err
		}
		if result.ScannedCount > options.Limit || len(result.Matches) > options.Limit {
			return errors.New("event preview exceeded the requested page limit")
		}
		if result.AppSlug != out.App || result.WorkflowName != out.Name {
			return errors.New("event preview scope mismatch")
		}
		out.Events = &result
		for _, match := range result.Matches {
			out.EventDecisions = append(out.EventDecisions, automationRecoveryEventDecision{EventID: match.EventID, Reason: automationRecoveryEventReason(*out, match)})
		}
		out.TriggerReason = "potential_admission_requires_current_gates"
	case "schedule":
		result, err := c.GetWorkflowSchedulePreview(ctx, out.App, out.Name, api.WorkflowSchedulePreviewOptions{At: options.Until, Since: &options.From, Count: 1})
		if err != nil {
			return err
		}
		if result.WorkflowName != out.Name {
			return errors.New("schedule preview scope mismatch")
		}
		out.Schedule = &result
		out.TriggerReason = "schedule_policy_preview"
		if out.FailurePaused {
			out.TriggerReason = "failure_resume_rearms_without_catch_up"
		}
	default:
		out.TriggerReason = "trigger_has_no_retained_work_preview"
	}
	return nil
}

func automationRecoveryEventReason(out automationRecoveryPreview, match api.WorkflowEventReplayPreviewMatch) string {
	switch {
	case match.AdmissionRecorded:
		return "already_admitted"
	case !match.FilterMatched:
		return "filter_mismatch"
	case out.FailurePaused:
		return "automatic_admission_failure_paused"
	case !out.Enabled:
		return "automatic_admission_disabled"
	default:
		return "potential_admission_requires_current_gates"
	}
}

func renderAutomationRecoveryPreview(w io.Writer, out automationRecoveryPreview) error {
	if _, err := fmt.Fprintf(w, "Recovery preview: %s / %s (version %d)\nRead-only observations; execution rechecks eligibility.\nAdmissions: enabled=%t, failure-paused=%t, generation=%d; retained events=%d.\n", out.App, out.Name, out.AutomationVersion, out.Enabled, out.FailurePaused, out.FailureGeneration, out.RetainedEvents); err != nil {
		return err
	}
	for _, page := range out.Runs {
		if _, err := fmt.Fprintf(w, "%s runs: showing %d of %d, offset %d\n", page.Status, len(page.Runs), page.Total, page.Offset); err != nil {
			return err
		}
		for _, run := range page.Runs {
			codes := []string{}
			for _, blocker := range run.Resume.Blockers {
				codes = append(codes, blocker.Code)
			}
			if _, err := fmt.Fprintf(w, "  %s: status=%s, resume-eligible=%t, expected-resume-count=%d, blockers=%s; reopen=%s; preserve=%s\n", run.ID, run.Status, run.Resume.Eligible, run.Resume.ExpectedResumeCount, strings.Join(codes, ","), strings.Join(run.Resume.ReopenedSteps, ","), strings.Join(run.Resume.PreservedSteps, ",")); err != nil {
				return err
			}
		}
		if page.NextOffset != nil {
			if _, err := fmt.Fprintf(w, "  More %s runs: use --run-offset %d\n", page.Status, *page.NextOffset); err != nil {
				return err
			}
		}
	}
	if out.Events != nil {
		e := out.Events
		if _, err := fmt.Fprintf(w, "Events [%s, %s): scanned=%d, potential admissions=%d, already admitted=%d; coverage=%s, history-complete=%t.\nPotential admissions are advisory and remain subject to pause, routing, quota and retention gates.\n", out.From.Format(time.RFC3339Nano), out.Until.Format(time.RFC3339Nano), e.ScannedCount, e.PotentialAdmissionCount, e.AlreadyAdmittedCount, e.Coverage, e.Retention.HistoryComplete); err != nil {
			return err
		}
		for _, match := range e.Matches {
			if _, err := fmt.Fprintf(w, "  %s: routing=%s, filter-matched=%t, admission-recorded=%t, reason=%s\n", match.EventID, match.RoutingState, match.FilterMatched, match.AdmissionRecorded, automationRecoveryEventReason(out, match)); err != nil {
				return err
			}
		}
		if e.NextAfter != "" {
			if _, err := fmt.Fprintf(w, "  More events: use --event-after %s with the same --from and --until\n", e.NextAfter); err != nil {
				return err
			}
		}
	}
	if out.Schedule != nil {
		s := out.Schedule
		if _, err := fmt.Fprintf(w, "Schedule: policy=%s, outcome=%s, eligible occurrences=%d, coalesced=%d, missed outside window=%t, missed not recovered=%t.\n", s.CatchUp.Policy, s.CatchUp.Outcome, s.CatchUp.EligibleOccurrences, s.CatchUp.CoalescedOccurrences, s.CatchUp.MissedOutsideWindow, s.CatchUp.MissedOccurrencesNotRecovered); err != nil {
			return err
		}
		if out.FailurePaused {
			if _, err := fmt.Fprintln(w, "Failure-pause resume re-arms schedule cursors without catch-up; this schedule preview is not a recovery promise."); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "Trigger reason: %s\nThis report spans multiple reads; it is not an atomic snapshot or a recovery reservation.\n", out.TriggerReason)
	return err
}
