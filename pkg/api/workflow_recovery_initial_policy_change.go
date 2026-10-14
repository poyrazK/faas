package api

// Initial candidates have failure identities, rather than invented parent jobs.
type WorkflowRecoveryInitialPolicyChangeCandidate struct {
	Failure     WorkflowInitialFailure     `json:"failure"`
	Eligibility string                     `json:"eligibility"`
	Preparation string                     `json:"preparation"`
	Execution   string                     `json:"execution"`
	Blockers    []string                   `json:"blockers"`
	Target      *WorkflowRunRecoveryTarget `json:"target,omitempty"`
}

// Counts describe the bounded sample. Pressure fields describe the current
// snapshot; candidate actions simulate processing that sample in order.
type WorkflowRecoveryInitialPolicyChangePreview struct {
	Coverage                     string                                         `json:"coverage"`
	InspectedFailures            int                                            `json:"inspected_failures"`
	Truncated                    bool                                           `json:"truncated"`
	Eligible                     int                                            `json:"eligible"`
	Excluded                     int                                            `json:"excluded"`
	SafetyBlocked                int                                            `json:"safety_blocked"`
	WouldPrepare                 int                                            `json:"would_prepare"`
	WouldSubmit                  int                                            `json:"would_submit"`
	RetainedReviews              int                                            `json:"retained_reviews"`
	RemainingReviewCapacity      int                                            `json:"remaining_review_capacity"`
	ActiveRecoveryJobs           int                                            `json:"active_recovery_jobs"`
	RemainingRecoveryJobCapacity int                                            `json:"remaining_recovery_job_capacity"`
	UnresolvedRecovery           bool                                           `json:"unresolved_recovery"`
	RuntimeAvailable             *bool                                          `json:"runtime_available,omitempty"`
	Candidates                   []WorkflowRecoveryInitialPolicyChangeCandidate `json:"candidates"`
}
