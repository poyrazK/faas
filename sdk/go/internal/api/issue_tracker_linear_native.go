package api

// NativeLinearIssueTrackerStatus is a credential-free preview scoped to a group and connection.
type NativeLinearIssueTrackerStatus struct {
	IssueID            string                     `json:"issue_id"`
	LinkID             string                     `json:"link_id"`
	ConnectionRevision int64                      `json:"connection_revision"`
	GroupFingerprint   string                     `json:"group_fingerprint"`
	WorkspaceID        string                     `json:"workspace_id"`
	TeamID             string                     `json:"team_id"`
	ViewerID           string                     `json:"viewer_id"`
	Status             IssueTrackerProviderStatus `json:"status"`
}

type UpdateNativeLinearIssueTrackerStatusRequest struct {
	DesiredState               string                     `json:"desired_state"`
	Expected                   IssueTrackerProviderStatus `json:"expected"`
	ExpectedConnectionRevision int64                      `json:"expected_connection_revision"`
	ExpectedGroupFingerprint   string                     `json:"expected_group_fingerprint"`
	LinearStateID              string                     `json:"linear_state_id,omitempty"`
}
