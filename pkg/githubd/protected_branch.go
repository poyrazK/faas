package githubd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

var ErrProtectedBranchEvidenceUnavailable = errors.New("githubd: protected branch evidence unavailable")

type ProtectedBranchClient interface {
	ProtectedBranchEvidence(context.Context, int64, int64, string, string, string) (githubdgrpc.ProtectedBranchEvidence, error)
}

type httpProtectedBranches struct {
	tokens *TokenCache
	client HTTPClient
}

func NewHTTPProtectedBranches(tokens *TokenCache, client HTTPClient) ProtectedBranchClient {
	if client == nil {
		client = NewHTTPClient()
	}
	return &httpProtectedBranches{tokens: tokens, client: client}
}

type protectedBranchHead struct {
	Name      string `json:"name"`
	Protected *bool  `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type protectedBranchSwitch struct {
	Enabled *bool `json:"enabled"`
}

type protectedBranchPolicy struct {
	EnforceAdmins   *protectedBranchSwitch `json:"enforce_admins"`
	AllowForcePush  *protectedBranchSwitch `json:"allow_force_pushes"`
	AllowDeletion   *protectedBranchSwitch `json:"allow_deletions"`
	RequiredReviews *struct {
		Count        int  `json:"required_approving_review_count"`
		DismissStale bool `json:"dismiss_stale_reviews"`
		LastPush     bool `json:"require_last_push_approval"`
		Bypass       *struct {
			Users []json.RawMessage `json:"users"`
			Teams []json.RawMessage `json:"teams"`
			Apps  []json.RawMessage `json:"apps"`
		} `json:"bypass_pull_request_allowances"`
	} `json:"required_pull_request_reviews"`
}

func (p protectedBranchPolicy) blockingReason() string {
	for _, value := range []*protectedBranchSwitch{p.EnforceAdmins, p.AllowForcePush, p.AllowDeletion} {
		if value == nil || value.Enabled == nil {
			return "protection_incomplete"
		}
	}
	switch {
	case !*p.EnforceAdmins.Enabled:
		return "admins_not_enforced"
	case *p.AllowForcePush.Enabled:
		return "force_push_allowed"
	case *p.AllowDeletion.Enabled:
		return "branch_deletion_allowed"
	case p.RequiredReviews == nil || p.RequiredReviews.Count < 1:
		return "reviews_not_required"
	case !p.RequiredReviews.DismissStale:
		return "stale_reviews_allowed"
	case !p.RequiredReviews.LastPush:
		return "last_push_approval_not_required"
	}
	if b := p.RequiredReviews.Bypass; b != nil && len(b.Users)+len(b.Teams)+len(b.Apps) != 0 {
		return "review_bypass_allowed"
	}
	return ""
}

func validProtectedBranchRequest(installationID, repositoryID int64, repository, branch, commitSHA string) bool {
	spec := api.EnvironmentGitSourceSpec{InstallationID: installationID, RepositoryID: repositoryID, Repository: repository,
		Ref: "refs/heads/" + branch, ManifestPath: "environment.yaml", Mode: "report", ApprovalPolicy: "protected_branch"}
	return spec.Validate() == nil && isCanonicalCommitSHA(commitSHA)
}

func (c *httpProtectedBranches) get(ctx context.Context, token, endpoint string, dst any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, ErrProtectedBranchEvidenceUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")
	resp, err := c.client.Do(req)
	if err != nil || resp == nil || resp.Body == nil {
		return false, ErrProtectedBranchEvidenceUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK || decodeGitHubJSON(resp.Body, dst) != nil {
		return false, ErrProtectedBranchEvidenceUnavailable
	}
	return true, nil
}

func protectedBranchPolicyDigest(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func (c *httpProtectedBranches) ProtectedBranchEvidence(ctx context.Context, installationID, repositoryID int64, repository, branch, commitSHA string) (githubdgrpc.ProtectedBranchEvidence, error) {
	reject := func(reason string) (githubdgrpc.ProtectedBranchEvidence, error) {
		return githubdgrpc.ProtectedBranchEvidence{Reason: reason}, nil
	}
	if c.tokens == nil || !validProtectedBranchRequest(installationID, repositoryID, repository, branch, commitSHA) {
		return githubdgrpc.ProtectedBranchEvidence{}, ErrProtectedBranchEvidenceUnavailable
	}
	// Bound the complete multi-request observation, including an injected client.
	ctx, cancel := context.WithTimeout(ctx, githubHTTPTimeout)
	defer cancel()
	token, err := c.tokens.Token(ctx, installationID)
	if err != nil || token == "" {
		return githubdgrpc.ProtectedBranchEvidence{}, ErrProtectedBranchEvidenceUnavailable
	}
	owner, repo, _ := strings.Cut(repository, "/")
	endpoint := GitHubAPI + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	var identity struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	}
	found, err := c.get(ctx, token, endpoint, &identity)
	if err != nil {
		return githubdgrpc.ProtectedBranchEvidence{}, err
	}
	if !found || identity.ID != repositoryID || !strings.EqualFold(identity.FullName, repository) {
		return reject("repository_identity_mismatch")
	}
	endpoint += "/branches/" + url.PathEscape(branch)
	digest := ""
	reviewCount := 0
	for pass := 0; pass < 2; pass++ {
		var head protectedBranchHead
		found, err := c.get(ctx, token, endpoint, &head)
		if err != nil {
			return githubdgrpc.ProtectedBranchEvidence{}, err
		}
		if !found {
			return reject("branch_not_found")
		}
		if head.Name != branch || !isCanonicalCommitSHA(head.Commit.SHA) || head.Protected == nil {
			return githubdgrpc.ProtectedBranchEvidence{}, ErrProtectedBranchEvidenceUnavailable
		}
		if head.Commit.SHA != commitSHA {
			return reject("branch_head_changed")
		}
		if !*head.Protected {
			return reject("branch_unprotected")
		}
		var raw json.RawMessage
		found, err = c.get(ctx, token, endpoint+"/protection", &raw)
		if err != nil {
			return githubdgrpc.ProtectedBranchEvidence{}, err
		}
		if !found {
			return reject("classic_protection_unavailable")
		}
		var policy protectedBranchPolicy
		if json.Unmarshal(raw, &policy) != nil {
			return githubdgrpc.ProtectedBranchEvidence{}, ErrProtectedBranchEvidenceUnavailable
		}
		if reason := policy.blockingReason(); reason != "" {
			return reject(reason)
		}
		next, err := protectedBranchPolicyDigest(raw)
		if err != nil {
			return githubdgrpc.ProtectedBranchEvidence{}, ErrProtectedBranchEvidenceUnavailable
		}
		if pass > 0 && next != digest {
			return reject("branch_protection_changed")
		}
		digest = next
		reviewCount = policy.RequiredReviews.Count
	}
	if ctx.Err() != nil {
		return githubdgrpc.ProtectedBranchEvidence{}, ErrProtectedBranchEvidenceUnavailable
	}
	return githubdgrpc.ProtectedBranchEvidence{Qualified: true, Profile: githubdgrpc.ProtectedBranchProfile, InstallationID: installationID,
		RepositoryID: repositoryID, Repository: repository, Branch: branch, CommitSHA: commitSHA, PolicyDigest: digest, RequiredReviewCount: reviewCount, CheckedAt: time.Now().UTC()}, nil
}
