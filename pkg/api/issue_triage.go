package api

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

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

// NormalizeBulkIssueAction validates before any mutation and canonicalizes UUIDs.
func NormalizeBulkIssueAction(in BulkIssueActionRequest, now time.Time) (BulkIssueActionRequest, error) {
	if len(in.IssueIDs) == 0 || len(in.IssueIDs) > IssueBulkActionMax {
		return in, fmt.Errorf("issue_ids must contain 1 to %d entries", IssueBulkActionMax)
	}
	in.IssueIDs = append([]string(nil), in.IssueIDs...)
	seen := map[uuid.UUID]bool{}
	for i, raw := range in.IssueIDs {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || seen[id] {
			return in, fmt.Errorf("issue_ids must contain distinct nonzero UUIDs")
		}
		seen[id] = true
		in.IssueIDs[i] = id.String()
	}
	a := &in.Action
	if a.TargetIssueID != "" || a.MemberIssueID != "" {
		return in, fmt.Errorf("bulk regrouping is unsupported")
	}
	if a.Action != "assign" && (a.AssigneeAccountID != "" || a.AssigneeTeamID != "") || a.Action != "resolve" && a.FixedDeploymentID != "" || a.Action != "ignore" && a.IgnoredUntil != nil {
		return in, fmt.Errorf("action contains unrelated fields")
	}
	switch a.Action {
	case "assign":
		account, team, err := NormalizeIssueAssignment(a.AssigneeAccountID, a.AssigneeTeamID)
		if err != nil {
			return in, err
		}
		a.AssigneeAccountID, a.AssigneeTeamID = account, team
	case "resolve":
		id, err := uuid.Parse(a.FixedDeploymentID)
		if err != nil || id == uuid.Nil {
			return in, fmt.Errorf("resolution requires a nonzero fixed_deployment_id")
		}
		a.FixedDeploymentID = id.String()
	case "ignore":
		if a.IgnoredUntil == nil || !a.IgnoredUntil.After(now) || a.IgnoredUntil.After(now.Add(IssueMaxTokenLifetime)) {
			return in, fmt.Errorf("ignored_until must be in the future and within 90 days")
		}
	case "reopen":
	default:
		return in, fmt.Errorf("bulk action must be assign, resolve, ignore, or reopen")
	}
	return in, nil
}

// BulkIssueLifecycleAction accepts only assign, resolve, ignore, or reopen.
type BulkIssueLifecycleAction = IssueActionRequest
