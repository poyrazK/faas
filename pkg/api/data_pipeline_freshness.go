package api

// DataPipelineFreshness reports checkpoint confirmation, never inferred time lag.
type DataPipelineFreshness struct {
	TargetSeconds          int64  `json:"target_seconds"`
	State                  string `json:"state"`
	ConfirmedThroughAt     string `json:"confirmed_through_at,omitempty"`
	ConfirmationAgeSeconds *int64 `json:"confirmation_age_seconds,omitempty"`
	Message                string `json:"message"`
}

type DataPipelineFreshnessRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
	TargetSeconds    int64 `json:"target_seconds"`
}
