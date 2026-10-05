// Package gitapproval holds credential-free evidence shared by Git readers and
// the customer-intent approval transaction. It never contacts Git providers.
package gitapproval

import (
	"regexp"
	"time"
)

const ProtectedBranchProfile = "classic_reviewed_branch/v1"
const ReviewedMergeProfile = "reviewed_merge/v1"

type PolicyEvidence struct {
	Qualified           bool      `json:"qualified"`
	Reason              string    `json:"reason,omitempty"`
	Profile             string    `json:"profile,omitempty"`
	InstallationID      int64     `json:"installation_id,omitempty"`
	RepositoryID        int64     `json:"repository_id,omitempty"`
	Repository          string    `json:"repository,omitempty"`
	Branch              string    `json:"branch,omitempty"`
	CommitSHA           string    `json:"commit_sha,omitempty"`
	PolicyDigest        string    `json:"policy_digest,omitempty"`
	RequiredReviewCount int       `json:"required_review_count,omitempty"`
	CheckedAt           time.Time `json:"checked_at,omitempty"`
}

var commitSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func fresh(checkedAt, now time.Time, maxAge time.Duration) bool {
	return !checkedAt.IsZero() && !now.IsZero() && maxAge > 0 && !checkedAt.After(now) && now.Sub(checkedAt) <= maxAge
}

func (e PolicyEvidence) ValidFor(installationID, repositoryID int64, repository, branch, sha string, now time.Time, maxAge time.Duration) bool {
	return e.Qualified && e.Reason == "" && e.Profile == ProtectedBranchProfile &&
		installationID > 0 && repositoryID > 0 && e.InstallationID == installationID && e.RepositoryID == repositoryID &&
		e.Repository == repository && e.Branch == branch && e.CommitSHA == sha && e.RequiredReviewCount > 0 &&
		commitSHA.MatchString(sha) && digest.MatchString(e.PolicyDigest) && fresh(e.CheckedAt, now, maxAge)
}

type ReviewEvidence struct {
	ID          int64     `json:"id"`
	ReviewerID  int64     `json:"reviewer_id"`
	Reviewer    string    `json:"reviewer"`
	HeadSHA     string    `json:"head_sha"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// MergeEvidence records approving reviews of the exact PR head before its
// merge, by distinct human users with repository write permission at check.
// Current policy is separate from review history; neither asserts what policy
// or permissions existed historically. GitHub attests the merged commit/PR link.
type MergeEvidence struct {
	ReviewedDefinitionDigest string           `json:"reviewed_definition_digest,omitempty"`
	Qualified                bool             `json:"qualified"`
	Reason                   string           `json:"reason,omitempty"`
	Profile                  string           `json:"profile,omitempty"`
	Policy                   PolicyEvidence   `json:"policy"`
	PullRequestID            int64            `json:"pull_request_id,omitempty"`
	PullRequestNumber        int64            `json:"pull_request_number,omitempty"`
	AuthorID                 int64            `json:"author_id,omitempty"`
	HeadSHA                  string           `json:"head_sha,omitempty"`
	MergedAt                 time.Time        `json:"merged_at,omitempty"`
	Reviews                  []ReviewEvidence `json:"reviews,omitempty"`
	CheckedAt                time.Time        `json:"checked_at,omitempty"`
}

func (e MergeEvidence) ValidFor(installationID, repositoryID int64, repository, branch, sha string, now time.Time, maxAge time.Duration) bool {
	if !e.Qualified || e.Reason != "" || e.Profile != ReviewedMergeProfile ||
		!e.Policy.ValidFor(installationID, repositoryID, repository, branch, sha, now, maxAge) ||
		e.PullRequestID <= 0 || e.PullRequestNumber <= 0 || e.AuthorID <= 0 || !commitSHA.MatchString(e.HeadSHA) ||
		e.MergedAt.IsZero() || e.MergedAt.After(e.CheckedAt) || e.CheckedAt.Before(e.Policy.CheckedAt) ||
		!fresh(e.CheckedAt, now, maxAge) || len(e.Reviews) < e.Policy.RequiredReviewCount {
		return false
	}
	users, reviews := map[int64]bool{}, map[int64]bool{}
	for _, review := range e.Reviews {
		if review.ID <= 0 || review.ReviewerID <= 0 || review.Reviewer == "" || review.ReviewerID == e.AuthorID ||
			users[review.ReviewerID] || reviews[review.ID] || review.HeadSHA != e.HeadSHA ||
			review.SubmittedAt.IsZero() || !review.SubmittedAt.Before(e.MergedAt) {
			return false
		}
		users[review.ReviewerID], reviews[review.ID] = true, true
	}
	return true
}
