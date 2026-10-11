package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/cronexpr"
)

type WorkflowScheduleFirePreview struct {
	ScheduledFor  time.Time `json:"scheduled_for"`
	LocalTime     string    `json:"local_time"`
	DSTAdjustment string    `json:"dst_adjustment,omitempty"`
}

type WorkflowScheduleDSTBehavior struct {
	Mode      string `json:"mode"`
	SpringGap string `json:"spring_gap"`
	FallFold  string `json:"fall_fold"`
}

type WorkflowScheduleCatchUpPreview struct {
	Policy                        string                       `json:"policy"`
	Window                        string                       `json:"window,omitempty"`
	Outcome                       string                       `json:"outcome"`
	Since                         *time.Time                   `json:"since,omitempty"`
	EligibleOccurrences           int                          `json:"eligible_occurrences"`
	CoalescedOccurrences          int                          `json:"coalesced_occurrences"`
	MissedOccurrencesNotRecovered bool                         `json:"missed_occurrences_not_recovered"`
	MissedOutsideWindow           bool                         `json:"missed_outside_window"`
	Selected                      *WorkflowScheduleFirePreview `json:"selected,omitempty"`
}

type WorkflowSchedulePreviewResponse struct {
	WorkflowName string                         `json:"workflow_name"`
	DeploymentID string                         `json:"deployment_id"`
	Schedule     string                         `json:"schedule"`
	Timezone     string                         `json:"timezone"`
	Overlap      string                         `json:"overlap"`
	Enabled      bool                           `json:"enabled"`
	ObservedAt   time.Time                      `json:"observed_at"`
	EvaluationAt time.Time                      `json:"evaluation_at"`
	DSTBehavior  WorkflowScheduleDSTBehavior    `json:"dst_behavior"`
	Upcoming     []WorkflowScheduleFirePreview  `json:"upcoming"`
	CatchUp      WorkflowScheduleCatchUpPreview `json:"catch_up"`
}

type WorkflowSchedulePreviewInput struct {
	WorkflowName  string
	DeploymentID  string
	Trigger       WorkflowTriggerSpec
	Enabled       bool
	ObservedAt    time.Time
	EvaluationAt  time.Time
	LastEvaluated *time.Time
	CursorMatches bool
	SinceOverride *time.Time
	Count         int
}

type WorkflowSchedulePreviewOptions struct {
	At    time.Time
	Since *time.Time
	Count int
}

func (c *Client) GetWorkflowSchedulePreview(ctx context.Context, slug, workflowName string, options WorkflowSchedulePreviewOptions) (WorkflowSchedulePreviewResponse, error) {
	var response WorkflowSchedulePreviewResponse
	path := workflowSchedulePreviewPath("/v1/apps/"+url.PathEscape(slug)+"/workflows/schedules/"+url.PathEscape(workflowName)+"/preview", options)
	err := c.do(ctx, "GET", path, nil, &response)
	return response, err
}

func (c *Client) GetPlatformTenantSelfWorkflowSchedulePreview(ctx context.Context, slug, workflowName string, options WorkflowSchedulePreviewOptions) (WorkflowSchedulePreviewResponse, error) {
	var response WorkflowSchedulePreviewResponse
	path := workflowSchedulePreviewPath("/v1/platform-tenant-self/apps/"+url.PathEscape(slug)+"/workflows/schedules/"+url.PathEscape(workflowName)+"/preview", options)
	err := c.do(ctx, "GET", path, nil, &response)
	return response, err
}

func workflowSchedulePreviewPath(path string, options WorkflowSchedulePreviewOptions) string {
	query := url.Values{}
	if !options.At.IsZero() {
		query.Set("at", options.At.Format(time.RFC3339Nano))
	}
	if options.Since != nil {
		query.Set("since", options.Since.Format(time.RFC3339Nano))
	}
	if options.Count > 0 {
		query.Set("count", strconv.Itoa(options.Count))
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return path
}

// WorkflowScheduleNominal applies the scheduler's exact current-minute and
// bounded latest-catch-up rules. The dispatcher and preview share this helper.
func WorkflowScheduleNominal(trigger WorkflowTriggerSpec, evaluated, now time.Time) (time.Time, error) {
	schedule, err := cronexpr.Parse(trigger.Schedule, trigger.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	nominal := now.UTC().Truncate(time.Minute)
	if schedule.Next(nominal.Add(-time.Minute)).Equal(nominal) {
		return nominal, nil
	}
	window, err := trigger.ScheduleCatchUpWindow()
	if err != nil || window == 0 {
		return time.Time{}, err
	}
	start := now.UTC().Add(-window).Add(-time.Nanosecond)
	if evaluated.After(start) {
		start = evaluated
	}
	var latest time.Time
	for fire := schedule.Next(start); !fire.IsZero() && !fire.After(nominal); fire = schedule.Next(fire) {
		latest = fire.UTC()
	}
	return latest, nil
}

// BuildWorkflowSchedulePreview simulates fire times and the next evaluator
// decision without changing the durable schedule cursor or admitting a run.
func BuildWorkflowSchedulePreview(input WorkflowSchedulePreviewInput) (WorkflowSchedulePreviewResponse, error) {
	var result WorkflowSchedulePreviewResponse
	if input.Count == 0 {
		input.Count = WorkflowSchedulePreviewDefaultCount
	}
	if input.Count < 1 || input.Count > WorkflowSchedulePreviewMaxCount {
		return result, fmt.Errorf("schedule preview count must be between 1 and %d", WorkflowSchedulePreviewMaxCount)
	}
	if input.Trigger.Type != "schedule" {
		return result, fmt.Errorf("workflow does not have a schedule trigger")
	}
	window, err := input.Trigger.ScheduleCatchUpWindow()
	if err != nil {
		return result, err
	}
	schedule, err := cronexpr.Parse(input.Trigger.Schedule, input.Trigger.Timezone)
	if err != nil {
		return result, err
	}
	timezone, err := cronexpr.NormalizeTimezone(input.Trigger.Timezone)
	if err != nil {
		return result, err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return result, err
	}
	if input.ObservedAt.IsZero() || input.EvaluationAt.IsZero() {
		return result, fmt.Errorf("schedule preview requires observation and evaluation times")
	}
	if input.SinceOverride != nil && input.SinceOverride.After(input.EvaluationAt) {
		return result, fmt.Errorf("schedule preview since time cannot be after evaluation time")
	}
	overlap := input.Trigger.Overlap
	if overlap == "" {
		overlap = "skip"
	}

	result = WorkflowSchedulePreviewResponse{
		WorkflowName: input.WorkflowName, DeploymentID: input.DeploymentID,
		Schedule: input.Trigger.Schedule, Timezone: timezone, Overlap: overlap, Enabled: input.Enabled,
		ObservedAt: input.ObservedAt.UTC(), EvaluationAt: input.EvaluationAt.UTC(),
		DSTBehavior: func() WorkflowScheduleDSTBehavior {
			mode, gap, fold := cronexpr.DSTPolicy(input.Trigger.Schedule)
			return WorkflowScheduleDSTBehavior{Mode: mode, SpringGap: gap, FallFold: fold}
		}(),
		Upcoming: make([]WorkflowScheduleFirePreview, 0, input.Count),
		CatchUp: WorkflowScheduleCatchUpPreview{
			Policy: input.Trigger.ScheduleCatchUpPolicy(),
		},
	}
	if window > 0 {
		result.CatchUp.Window = window.String()
	}
	after := input.EvaluationAt
	for range input.Count {
		next := schedule.Next(after)
		if next.IsZero() {
			break
		}
		result.Upcoming = append(result.Upcoming, workflowScheduleFirePreview(input.Trigger, next, location))
		after = next
	}

	since := input.LastEvaluated
	if input.SinceOverride != nil {
		since = input.SinceOverride
	}
	if since != nil {
		value := since.UTC()
		result.CatchUp.Since = &value
	}
	switch {
	case !input.Enabled:
		result.CatchUp.Outcome = "disabled"
		return result, nil
	case since == nil || !input.CursorMatches && input.SinceOverride == nil:
		result.CatchUp.Outcome = "first_evaluation_arms"
		return result, nil
	}
	nominal := input.EvaluationAt.UTC().Truncate(time.Minute)
	if !nominal.After(*since) {
		result.CatchUp.Outcome = "no_new_evaluation"
		return result, nil
	}
	selected, err := WorkflowScheduleNominal(input.Trigger, *since, input.EvaluationAt)
	if err != nil {
		return result, err
	}
	if selected.IsZero() {
		missed := schedule.Next(*since)
		switch {
		case result.CatchUp.Policy == WorkflowScheduleCatchUpLatest && !missed.IsZero() && missed.Before(input.EvaluationAt.Add(-window)):
			result.CatchUp.Outcome = "outside_catch_up_window"
			result.CatchUp.MissedOutsideWindow = true
		case result.CatchUp.Policy == WorkflowScheduleCatchUpSkip && !missed.IsZero() && missed.Before(nominal):
			result.CatchUp.Outcome = "skip_missed"
			result.CatchUp.MissedOccurrencesNotRecovered = true
		default:
			result.CatchUp.Outcome = "no_due_occurrence"
		}
		return result, nil
	}
	selectedPreview := workflowScheduleFirePreview(input.Trigger, selected, location)
	result.CatchUp.Selected = &selectedPreview
	if selected.Equal(nominal) {
		result.CatchUp.Outcome = "run_current_fire"
	} else {
		result.CatchUp.Outcome = "coalesce_latest"
	}
	if result.CatchUp.Policy == WorkflowScheduleCatchUpLatest {
		start := input.EvaluationAt.UTC().Add(-window).Add(-time.Nanosecond)
		if since.After(start) {
			start = *since
		}
		for fire := schedule.Next(start); !fire.IsZero() && !fire.After(nominal); fire = schedule.Next(fire) {
			result.CatchUp.EligibleOccurrences++
		}
		if result.CatchUp.EligibleOccurrences > 1 {
			result.CatchUp.CoalescedOccurrences = result.CatchUp.EligibleOccurrences - 1
		}
		first := schedule.Next(*since)
		result.CatchUp.MissedOutsideWindow = !first.IsZero() && first.Before(input.EvaluationAt.UTC().Add(-window))
	} else {
		first := schedule.Next(*since)
		result.CatchUp.EligibleOccurrences = 1
		result.CatchUp.MissedOccurrencesNotRecovered = !first.IsZero() && first.Before(nominal)
	}
	return result, nil
}

func workflowScheduleFirePreview(trigger WorkflowTriggerSpec, fire time.Time, location *time.Location) WorkflowScheduleFirePreview {
	result := WorkflowScheduleFirePreview{ScheduledFor: fire.UTC(), LocalTime: fire.In(location).Format(time.RFC3339)}
	nominal, err := cronexpr.NominalOccurrence(trigger.Schedule, trigger.Timezone, fire)
	if err == nil && !nominal {
		result.DSTAdjustment = "shifted_forward"
	}
	return result
}
