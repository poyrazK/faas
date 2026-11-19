package mcphosting

import "github.com/onebox-faas/faas/pkg/api"

type TaskReleaseReview = api.TaskReleaseReview
type TaskReviewLimits = api.TaskReviewLimits

// ReviewTaskLimits omits all credential references from configured Task limits.
func ReviewTaskLimits(c TasksConfig) TaskReviewLimits {
	return TaskReviewLimits{TTLSeconds: c.TTLSeconds, WorkerConcurrency: c.WorkerConcurrency,
		MaxAttempts: c.MaxAttempts, MaxRunning: c.MaxRunning, MaxRunningPerOwner: c.MaxRunningPerOwner,
		MaxOutstanding: c.MaxOutstanding, MaxOutstandingPerOwner: c.MaxOutstandingPerOwner,
		ShutdownTimeoutMS: c.ShutdownTimeoutMS}
}
