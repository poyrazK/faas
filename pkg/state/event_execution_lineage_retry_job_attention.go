package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func lineageRetryJobAttentionOption(status string, options []api.EventExecutionLineageRetryJobAttentionOptions) (string, error) {
	if len(options) > 1 {
		return "", ErrInvalidArgument
	}
	var option api.EventExecutionLineageRetryJobAttentionOptions
	if len(options) == 1 {
		option = options[0]
	}
	if option.Validate(status) != nil {
		return "", ErrInvalidArgument
	}
	return option.Attention, nil
}
func lineageRetryJobMatchesAttention(status string, created, updated, retain time.Time, attention string, now time.Time) bool {
	if attention == "" {
		return true
	}
	if (status != "pending" && status != "paused") || created.After(now) {
		return false
	}
	stalled := status == "pending" && !updated.After(now.Add(-api.EventExecutionLineageRetryInactivityWarning))
	risk := !retain.After(now.Add(api.EventExecutionLineageRetryRetentionWarning))
	return attention == "any" && (stalled || risk) || attention == "stalled" && stalled || attention == "retention_risk" && risk
}
func copyLineageProgressTime(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	copy := *at
	return &copy
}

func lineageRetryJobAttentionResult(jobs []api.EventExecutionLineageRetryJobSummary, limit int, attention string, now time.Time) (api.EventExecutionLineageRetryJobs, error) {
	out := lineageRetryJobsResult(jobs, limit)
	if attention == "" {
		return out, nil
	}
	out.Attention = &api.EventExecutionLineageRetryJobAttentionPage{Coverage: "live_matching_retry_jobs_page", Filter: attention, ObservedAt: now, HasMore: out.NextAfter != "", InactivityWarningSeconds: int64(api.EventExecutionLineageRetryInactivityWarning / time.Second), RetentionWarningSeconds: int64(api.EventExecutionLineageRetryRetentionWarning / time.Second)}
	for i, job := range out.Jobs {
		observation := lineageRetryHealthObservation{id: job.RequestID, status: job.Status, processed: job.Processed, selected: job.Selected, created: job.CreatedAt, activity: job.UpdatedAt, retain: job.RetainUntil, progress: job.LastProgressAt}
		health, err := buildLineageRetryJobHealth([]lineageRetryHealthObservation{observation}, now)
		if err != nil {
			return out, err
		}
		sample := health.Jobs[0]
		out.Jobs[i].Attention = &sample
	}
	return out, nil
}
