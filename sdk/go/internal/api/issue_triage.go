package api

import "time"

// IssueReleaseHealth describes retained observations, not complete traffic or sessions.
type IssueReleaseHealth struct {
	DeploymentID         string    `json:"deployment_id"`
	CommitSHA            string    `json:"commit_sha"`
	CreatedAt            time.Time `json:"created_at"`
	ObservedRequests     int64     `json:"observed_requests"`
	HTTP5xxRequests      int64     `json:"http_5xx_requests"`
	HTTP5xxRate          *float64  `json:"http_5xx_rate"`
	RequestCoverage      string    `json:"request_coverage"`
	ObservedIssueEvents  int64     `json:"observed_issue_events"`
	ObservedIssueGroups  int64     `json:"observed_issue_groups"`
	NewIssueGroups       int64     `json:"new_issue_groups"`
	RegressedIssueGroups int64     `json:"regressed_issue_groups"`
	IdentifiedCustomers  int64     `json:"identified_customers"`
	UnattributedEvents   int64     `json:"unattributed_events"`
}

type IssueReleaseComparison struct {
	WindowStart      time.Time          `json:"window_start"`
	WindowEnd        time.Time          `json:"window_end"`
	Environment      string             `json:"environment"`
	Baseline         IssueReleaseHealth `json:"baseline"`
	Candidate        IssueReleaseHealth `json:"candidate"`
	HTTP5xxRateDelta *float64           `json:"http_5xx_rate_delta"`
}

type BulkIssueActionRequest struct {
	IssueIDs []string           `json:"issue_ids"`
	Action   IssueActionRequest `json:"action"`
}

type BulkIssueActionResponse struct {
	Items []Issue `json:"items"`
}

// BulkIssueLifecycleAction accepts only assign, resolve, ignore, or reopen.
type BulkIssueLifecycleAction = IssueActionRequest
