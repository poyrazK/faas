package githubd

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitapproval"
)

type ReviewedMergeClient interface {
	ReviewedMergeEvidence(context.Context, int64, int64, string, string, string) (gitapproval.MergeEvidence, error)
}

func NewHTTPReviewedMerges(tokens *TokenCache, client HTTPClient) ReviewedMergeClient {
	if client == nil {
		client = NewHTTPClient()
	}
	return &httpProtectedBranches{tokens: tokens, client: client}
}

type gitReviewUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

type gitMergedPR struct {
	ID       int64         `json:"id"`
	Number   int64         `json:"number"`
	Merged   bool          `json:"merged"`
	State    string        `json:"state"`
	MergeSHA string        `json:"merge_commit_sha"`
	MergedAt time.Time     `json:"merged_at"`
	User     gitReviewUser `json:"user"`
	Base     struct {
		Ref  string `json:"ref"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		SHA  string `json:"sha"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"head"`
}

type gitReview struct {
	ID          int64         `json:"id"`
	State       string        `json:"state"`
	CommitSHA   string        `json:"commit_id"`
	SubmittedAt time.Time     `json:"submitted_at"`
	User        gitReviewUser `json:"user"`
}

func (c *httpProtectedBranches) findMergedPR(ctx context.Context, token, endpoint string, repositoryID int64, branch, sha string) (gitMergedPR, string, error) {
	var associated []gitMergedPR
	found, err := c.get(ctx, token, fmt.Sprintf("%s/commits/%s/pulls?per_page=%d&page=1", endpoint, sha, api.EnvironmentGitApprovalPageSize), &associated)
	if err != nil {
		return gitMergedPR{}, "", err
	}
	if !found || associated == nil {
		return gitMergedPR{}, "merged_pull_request_unavailable", nil
	}
	if len(associated) >= api.EnvironmentGitApprovalMaxAssociatedPRs {
		return gitMergedPR{}, "pull_request_history_too_large", nil
	}
	var candidate gitMergedPR
	for _, pr := range associated {
		if pr.MergeSHA != sha || pr.Base.Repo.ID != repositoryID || pr.Base.Ref != branch {
			continue
		}
		if candidate.ID != 0 || pr.ID <= 0 || pr.Number <= 0 {
			return gitMergedPR{}, "merged_pull_request_ambiguous", nil
		}
		candidate = pr
	}
	if candidate.ID == 0 {
		return gitMergedPR{}, "merged_pull_request_unavailable", nil
	}
	var pr gitMergedPR
	found, err = c.get(ctx, token, fmt.Sprintf("%s/pulls/%d", endpoint, candidate.Number), &pr)
	if err != nil {
		return gitMergedPR{}, "", err
	}
	if !found || pr.ID != candidate.ID || pr.Number != candidate.Number || !pr.Merged || pr.State != "closed" ||
		pr.MergeSHA != sha || pr.Base.Repo.ID != repositoryID || pr.Base.Ref != branch || pr.User.ID <= 0 ||
		!isCanonicalCommitSHA(pr.Head.SHA) || pr.MergedAt.IsZero() || pr.MergedAt.After(time.Now()) {
		return gitMergedPR{}, "merged_pull_request_unavailable", nil
	}
	if pr.Head.Repo.ID != repositoryID {
		return gitMergedPR{}, "review_head_repository_unavailable", nil
	}
	return pr, "", nil
}

func (c *httpProtectedBranches) reviewedHead(ctx context.Context, token, endpoint string, pr gitMergedPR) ([]gitReview, string, error) {
	latest, seen := map[int64]gitReview{}, map[int64]bool{}
	total := 0
	for page := 1; ; page++ {
		var rows []gitReview
		found, err := c.get(ctx, token, fmt.Sprintf("%s/pulls/%d/reviews?per_page=%d&page=%d", endpoint, pr.Number, api.EnvironmentGitApprovalPageSize, page), &rows)
		if err != nil {
			return nil, "", err
		}
		if !found || rows == nil {
			return nil, "review_history_unavailable", nil
		}
		total += len(rows)
		if total > api.EnvironmentGitApprovalMaxReviews || page > api.EnvironmentGitApprovalMaxReviews/api.EnvironmentGitApprovalPageSize+1 {
			return nil, "review_history_too_large", nil
		}
		for _, review := range rows {
			if review.ID <= 0 || seen[review.ID] {
				return nil, "review_history_incomplete", nil
			}
			seen[review.ID] = true
			if review.State == "PENDING" || review.State == "COMMENTED" {
				continue
			}
			if review.User.ID <= 0 || review.User.Login == "" || review.SubmittedAt.IsZero() ||
				(review.State != "APPROVED" && review.State != "CHANGES_REQUESTED" && review.State != "DISMISSED") {
				return nil, "review_history_incomplete", nil
			}
			// Equality at the provider's timestamp precision cannot prove before-merge order.
			if !review.SubmittedAt.Before(pr.MergedAt) {
				continue
			}
			previous, exists := latest[review.User.ID]
			if !exists || review.SubmittedAt.After(previous.SubmittedAt) || review.SubmittedAt.Equal(previous.SubmittedAt) && review.ID > previous.ID {
				latest[review.User.ID] = review
			}
		}
		if len(rows) < api.EnvironmentGitApprovalPageSize {
			break
		}
	}
	ordered := make([]gitReview, 0, len(latest))
	for _, review := range latest {
		ordered = append(ordered, review)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].User.ID < ordered[j].User.ID })
	return ordered, "", nil
}

func (c *httpProtectedBranches) reviewerCanApprove(ctx context.Context, token, endpoint string, user gitReviewUser) (bool, error) {
	if user.Type != "User" {
		return false, nil
	}
	var permission struct {
		Permission string        `json:"permission"`
		User       gitReviewUser `json:"user"`
	}
	found, err := c.get(ctx, token, endpoint+"/collaborators/"+url.PathEscape(user.Login)+"/permission", &permission)
	if err != nil {
		return false, err
	}
	return found && permission.User.ID == user.ID && permission.User.Type == "User" && strings.EqualFold(permission.User.Login, user.Login) &&
		(permission.Permission == "write" || permission.Permission == "admin"), nil
}

func (c *httpProtectedBranches) ReviewedMergeEvidence(ctx context.Context, installationID, repositoryID int64, repository, branch, sha string) (gitapproval.MergeEvidence, error) {
	reject := func(reason string) (gitapproval.MergeEvidence, error) {
		return gitapproval.MergeEvidence{Reason: reason}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.EnvironmentGitReviewedMergeReadTimeout)
	defer cancel()
	policy, err := c.ProtectedBranchEvidence(ctx, installationID, repositoryID, repository, branch, sha)
	if err != nil {
		return gitapproval.MergeEvidence{}, err
	}
	if !policy.Qualified {
		return reject(policy.Reason)
	}
	token, err := c.tokens.Token(ctx, installationID)
	if err != nil {
		return gitapproval.MergeEvidence{}, ErrProtectedBranchEvidenceUnavailable
	}
	owner, repo, _ := strings.Cut(repository, "/")
	endpoint := GitHubAPI + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	pr, reason, err := c.findMergedPR(ctx, token, endpoint, repositoryID, branch, sha)
	if err != nil {
		return gitapproval.MergeEvidence{}, err
	}
	if reason != "" {
		return reject(reason)
	}
	reviews, reason, err := c.reviewedHead(ctx, token, endpoint, pr)
	if err != nil {
		return gitapproval.MergeEvidence{}, err
	}
	if reason != "" {
		return reject(reason)
	}
	approved := make([]gitapproval.ReviewEvidence, 0)
	for _, review := range reviews {
		if review.User.ID == pr.User.ID || review.State == "DISMISSED" {
			continue
		}
		eligible, err := c.reviewerCanApprove(ctx, token, endpoint, review.User)
		if err != nil {
			return gitapproval.MergeEvidence{}, err
		}
		if eligible && review.State == "CHANGES_REQUESTED" {
			return reject("review_changes_requested")
		}
		if eligible && review.State == "APPROVED" && review.CommitSHA == pr.Head.SHA {
			approved = append(approved, gitapproval.ReviewEvidence{ID: review.ID, ReviewerID: review.User.ID, Reviewer: review.User.Login,
				HeadSHA: review.CommitSHA, SubmittedAt: review.SubmittedAt})
		}
	}
	if len(approved) < policy.RequiredReviewCount {
		return reject("review_approvals_insufficient")
	}
	current, err := c.ProtectedBranchEvidence(ctx, installationID, repositoryID, repository, branch, sha)
	if err != nil {
		return gitapproval.MergeEvidence{}, err
	}
	if !current.Qualified || current.PolicyDigest != policy.PolicyDigest || current.RequiredReviewCount != policy.RequiredReviewCount {
		return reject("branch_protection_changed")
	}
	evidence := gitapproval.MergeEvidence{Qualified: true, Profile: gitapproval.ReviewedMergeProfile, Policy: current, PullRequestID: pr.ID,
		PullRequestNumber: pr.Number, AuthorID: pr.User.ID, HeadSHA: pr.Head.SHA, MergedAt: pr.MergedAt,
		Reviews: approved[:policy.RequiredReviewCount], CheckedAt: time.Now().UTC()}
	if ctx.Err() != nil || !evidence.ValidFor(installationID, repositoryID, repository, branch, sha, time.Now(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
		return gitapproval.MergeEvidence{}, ErrProtectedBranchEvidenceUnavailable
	}
	return evidence, nil
}
