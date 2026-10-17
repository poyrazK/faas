package api

type IssueTrackerProviderStatus struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	ID       string `json:"id"`
	State    string `json:"state"`
	TeamID   string `json:"team_id,omitempty"`
	StateID  string `json:"state_id,omitempty"`
}
type NativeIssueTrackerStatus struct {
	IssueID        string                     `json:"issue_id"`
	LinkID         string                     `json:"link_id"`
	InstallationID int64                      `json:"installation_id"`
	Repository     string                     `json:"repository"`
	RepositoryID   int64                      `json:"repository_id"`
	Status         IssueTrackerProviderStatus `json:"status"`
}
type UpdateNativeIssueTrackerStatusRequest struct {
	DesiredState string                     `json:"desired_state"`
	Expected     IssueTrackerProviderStatus `json:"expected"`
}
