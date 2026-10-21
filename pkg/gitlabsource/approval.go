package gitlabsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/gitapproval"
)

// The initial GitLab profile deliberately supports one project-wide approval
// rule. Hidden/custom/scoped rules cannot be flattened into a review count.
// All matching branch protections are checked because GitLab uses the most
// permissive push rule, including inherited and wildcard rules.
type branchProtection struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	ForcePush *bool          `json:"allow_force_push"`
	Push      []branchAccess `json:"push_access_levels"`
	Merge     []branchAccess `json:"merge_access_levels"`
}
type branchAccess struct {
	Level        *int   `json:"access_level"`
	UserID       *int64 `json:"user_id"`
	GroupID      *int64 `json:"group_id"`
	DeployKeyID  *int64 `json:"deploy_key_id"`
	MemberRoleID *int64 `json:"member_role_id"`
}
type approvalConfig struct {
	Reset       *bool `json:"reset_approvals_on_push"`
	Selective   *bool `json:"selective_code_owner_removals"`
	NoOverride  *bool `json:"disable_overriding_approvers_per_merge_request"`
	Author      *bool `json:"merge_requests_author_approval"`
	NoCommitter *bool `json:"merge_requests_disable_committers_approval"`
}
type approvalUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	State    string `json:"state"`
	Bot      *bool  `json:"bot"`
}
type approvalRule struct {
	Users    []approvalUser `json:"users"`
	Eligible []approvalUser `json:"eligible_approvers"`
	Groups   []struct {
		ID int64 `json:"id"`
	} `json:"groups"`
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"rule_type"`
	Required   int               `json:"approvals_required"`
	Hidden     *bool             `json:"contains_hidden_groups"`
	Protected  []json.RawMessage `json:"protected_branches"`
	Approved   *bool             `json:"approved"`
	Overridden *bool             `json:"overridden"`
	By         []approvalUser    `json:"approved_by"`
	SourceRule *struct {
		ID int64 `json:"id"`
	} `json:"source_rule"`
}
type mergeRequest struct {
	ID              int64        `json:"id"`
	IID             int64        `json:"iid"`
	ProjectID       int64        `json:"project_id"`
	SourceProjectID int64        `json:"source_project_id"`
	TargetProjectID int64        `json:"target_project_id"`
	TargetBranch    string       `json:"target_branch"`
	State           string       `json:"state"`
	Imported        bool         `json:"imported"`
	Draft           bool         `json:"draft"`
	SHA             string       `json:"sha"`
	MergeSHA        string       `json:"merge_commit_sha"`
	SquashSHA       string       `json:"squash_commit_sha"`
	MergedAt        time.Time    `json:"merged_at"`
	Author          approvalUser `json:"author"`
	DiffRefs        struct {
		Head string `json:"head_sha"`
	} `json:"diff_refs"`
}
type diffVersion struct {
	ID             int64     `json:"id"`
	MergeRequestID int64     `json:"merge_request_id"`
	Head           string    `json:"head_commit_sha"`
	CreatedAt      time.Time `json:"created_at"`
	State          string    `json:"state"`
	Patch          string    `json:"patch_id_sha"`
}

func boolIs(v *bool, want bool) bool { return v != nil && *v == want }

// readJSON bounds provider responses and rejects trailing/truncated documents.
func (c Client) readJSON(ctx context.Context, token, path string, out any) (string, error) {
	response, err := c.get(ctx, token, path)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	const max = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || len(data) > max || json.Unmarshal(data, out) != nil {
		return "", ErrUnavailable
	}
	return response.Header.Get("X-Next-Page"), nil
}

func readList[T any](ctx context.Context, c Client, token, path string) ([]T, error) {
	var all []T
	for page := 1; page <= 10; page++ {
		var rows []T
		next, err := c.readJSON(ctx, token, path+"?per_page=100&page="+strconv.Itoa(page), &rows)
		if err != nil || rows == nil {
			return nil, ErrUnavailable
		}
		all = append(all, rows...)
		if next == "" && len(rows) < 100 {
			return all, nil
		}
		if next != "" && next != strconv.Itoa(page+1) {
			return nil, ErrUnavailable
		}
	}
	return nil, ErrUnavailable
}

func matchesBranch(pattern, branch string) bool {
	expression := "^" + regexp.QuoteMeta(pattern) + "$"
	expression = regexp.MustCompile(`\\\*`).ReplaceAllString(expression, ".*")
	matched, _ := regexp.MatchString(expression, branch)
	return matched
}

func (c Client) reviewedPolicy(ctx context.Context, token string, projectID int64, repository, branch, sha string) (gitapproval.PolicyEvidence, approvalRule, error) {
	prefix := "/projects/" + strconv.FormatInt(projectID, 10)
	var config approvalConfig
	if _, err := c.readJSON(ctx, token, prefix+"/approvals", &config); err != nil {
		return gitapproval.PolicyEvidence{}, approvalRule{}, err
	}
	if !boolIs(config.Reset, true) || !boolIs(config.Selective, false) || !boolIs(config.NoOverride, true) || !boolIs(config.Author, false) || !boolIs(config.NoCommitter, true) {
		return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
	}
	protections, err := readList[branchProtection](ctx, c, token, prefix+"/protected_branches")
	if err != nil {
		return gitapproval.PolicyEvidence{}, approvalRule{}, err
	}
	var matching []branchProtection
	for _, protection := range protections {
		if protection.Name == "" || len(protection.Name) > 512 || strings.ContainsAny(protection.Name, "?[]\\") {
			return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
		}
		if !matchesBranch(protection.Name, branch) {
			continue
		}
		if protection.ID <= 0 || !boolIs(protection.ForcePush, false) || len(protection.Push) == 0 || len(protection.Merge) == 0 {
			return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
		}
		for _, access := range protection.Push {
			if access.Level == nil || *access.Level != 0 || access.UserID != nil || access.GroupID != nil || access.DeployKeyID != nil || access.MemberRoleID != nil {
				return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
			}
		}
		for _, access := range protection.Merge {
			if access.Level == nil || (*access.Level != 30 && *access.Level != 40 && *access.Level != 50) || access.UserID != nil || access.GroupID != nil || access.DeployKeyID != nil || access.MemberRoleID != nil {
				return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
			}
		}
		matching = append(matching, protection)
	}
	if len(matching) == 0 {
		return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
	}
	rules, err := readList[approvalRule](ctx, c, token, prefix+"/approval_rules")
	if err != nil || len(rules) != 1 {
		return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
	}
	rule := rules[0]
	if rule.ID <= 0 || rule.Name == "" || (rule.Type != "regular" && rule.Type != "any_approver") || rule.Required <= 0 || !boolIs(rule.Hidden, false) || len(rule.Protected) != 0 {
		return gitapproval.PolicyEvidence{}, approvalRule{}, ErrUnavailable
	}
	slices.SortFunc(matching, func(a, b branchProtection) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	data, _ := json.Marshal(struct {
		Config   approvalConfig
		Branches []branchProtection
		Rule     approvalRule
	}{config, matching, rule})
	sum := sha256.Sum256(data)
	return gitapproval.PolicyEvidence{Qualified: true, Profile: gitapproval.GitLabProtectedBranchProfile, RepositoryID: projectID, Repository: repository, Branch: branch, CommitSHA: sha, RequiredReviewCount: rule.Required, PolicyDigest: hex.EncodeToString(sum[:]), CheckedAt: time.Now().UTC()}, rule, nil
}

// ReviewedMergeEvidence uses only provider-authenticated history. Approvals
// must follow the last diff version and precede the merge. Current policy and
// reviewer permissions are attested at observation time, never historically.
func (c Client) ReviewedMergeEvidence(ctx context.Context, token string, projectID int64, repository, branch, sha string) (gitapproval.MergeEvidence, error) {
	var empty gitapproval.MergeEvidence
	if projectID <= 0 || !ValidRepositoryPath(repository) || branch == "" || !validSHA(sha) {
		return empty, ErrUnavailable
	}
	if _, err := c.VerifyRepository(ctx, token, repository, projectID); err != nil {
		return empty, err
	}
	policy, rule, err := c.reviewedPolicy(ctx, token, projectID, repository, branch, sha)
	if err != nil {
		return empty, err
	}
	prefix := "/projects/" + strconv.FormatInt(projectID, 10)
	requests, err := readList[mergeRequest](ctx, c, token, prefix+"/repository/commits/"+sha+"/merge_requests")
	if err != nil {
		return empty, err
	}
	var candidates []mergeRequest
	for _, mr := range requests {
		if mr.State == "merged" && mr.TargetProjectID == projectID && mr.TargetBranch == branch && (mr.MergeSHA == sha || mr.SquashSHA == sha || mr.SHA == sha && mr.MergeSHA == "" && mr.SquashSHA == "") {
			candidates = append(candidates, mr)
		}
	}
	if len(candidates) != 1 {
		return empty, ErrUnavailable
	}
	path := prefix + "/merge_requests/" + strconv.FormatInt(candidates[0].IID, 10)
	var mr mergeRequest
	if _, err = c.readJSON(ctx, token, path, &mr); err != nil {
		return empty, err
	}
	if mr.ID != candidates[0].ID || mr.IID <= 0 || mr.ProjectID != projectID || mr.TargetProjectID != projectID || mr.SourceProjectID != projectID || mr.TargetBranch != branch || mr.State != "merged" || mr.Imported || mr.Draft || mr.Author.ID <= 0 || !validSHA(mr.SHA) || mr.DiffRefs.Head != mr.SHA || mr.MergedAt.IsZero() || (mr.MergeSHA != sha && mr.SquashSHA != sha && (mr.MergeSHA != "" || mr.SquashSHA != "" || mr.SHA != sha)) {
		return empty, ErrUnavailable
	}
	versions, err := readList[diffVersion](ctx, c, token, path+"/versions")
	if err != nil || len(versions) == 0 {
		return empty, ErrUnavailable
	}
	slices.SortFunc(versions, func(a, b diffVersion) int { return b.CreatedAt.Compare(a.CreatedAt) })
	version := versions[0]
	if version.ID <= 0 || version.MergeRequestID != mr.ID || version.Head != mr.SHA || version.State != "collected" || version.Patch == "" || version.CreatedAt.IsZero() || !version.CreatedAt.Before(mr.MergedAt) {
		return empty, ErrUnavailable
	}
	var approvals struct {
		Approved *bool `json:"approved"`
		Required int   `json:"approvals_required"`
		Left     int   `json:"approvals_left"`
		By       []struct {
			User approvalUser `json:"user"`
			At   time.Time    `json:"approved_at"`
		} `json:"approved_by"`
	}
	if _, err = c.readJSON(ctx, token, path+"/approvals", &approvals); err != nil {
		return empty, err
	}
	var state struct {
		Overwritten *bool          `json:"approval_rules_overwritten"`
		Rules       []approvalRule `json:"rules"`
	}
	if _, err = c.readJSON(ctx, token, path+"/approval_state", &state); err != nil {
		return empty, err
	}
	if !boolIs(approvals.Approved, true) || approvals.Left != 0 || approvals.Required != rule.Required || !boolIs(state.Overwritten, false) || len(state.Rules) != 1 {
		return empty, ErrUnavailable
	}
	effective := state.Rules[0]
	if effective.SourceRule == nil || effective.SourceRule.ID != rule.ID || effective.Required != rule.Required || effective.Type != rule.Type || !boolIs(effective.Approved, true) || !boolIs(effective.Overridden, false) || !boolIs(effective.Hidden, false) {
		return empty, ErrUnavailable
	}
	evidence := gitapproval.MergeEvidence{Qualified: true, Profile: gitapproval.GitLabReviewedMergeProfile, Policy: policy, PullRequestID: mr.ID, PullRequestNumber: mr.IID, AuthorID: mr.Author.ID, HeadSHA: mr.SHA, MergedAt: mr.MergedAt}
	allowed := map[int64]bool{}
	for _, user := range effective.By {
		allowed[user.ID] = true
	}
	seen := map[int64]bool{}
	for _, approval := range approvals.By {
		user := approval.User
		if user.ID <= 0 || seen[user.ID] || !allowed[user.ID] || user.ID == mr.Author.ID || !approval.At.After(version.CreatedAt) || !approval.At.Before(mr.MergedAt) {
			return empty, ErrUnavailable
		}
		seen[user.ID] = true
		var current approvalUser
		if _, err = c.readJSON(ctx, token, "/users/"+strconv.FormatInt(user.ID, 10), &current); err != nil {
			return empty, err
		}
		var member struct {
			ID      int64  `json:"id"`
			Level   int    `json:"access_level"`
			State   string `json:"state"`
			Expires string `json:"expires_at"`
		}
		if _, err = c.readJSON(ctx, token, prefix+"/members/all/"+strconv.FormatInt(user.ID, 10), &member); err != nil {
			return empty, err
		}
		if current.ID != user.ID || current.Username == "" || current.State != "active" || !boolIs(current.Bot, false) || member.ID != user.ID || (member.Level != 30 && member.Level != 40 && member.Level != 50) || member.State != "active" {
			return empty, ErrUnavailable
		}
		if member.Expires != "" {
			expiry, err := time.Parse("2006-01-02", member.Expires)
			if err != nil || !expiry.After(time.Now().UTC()) {
				return empty, ErrUnavailable
			}
		}
		// GitLab approvals are unique per user rather than separate review objects.
		evidence.Reviews = append(evidence.Reviews, gitapproval.ReviewEvidence{ID: user.ID, ReviewerID: user.ID, Reviewer: current.Username, HeadSHA: mr.SHA, SubmittedAt: approval.At})
	}
	// Recheck project identity, policy, ref and merge frame after the multi-call
	// observation. A moving branch or changed rule invalidates the whole receipt.
	again, _, err := c.reviewedPolicy(ctx, token, projectID, repository, branch, sha)
	if err != nil || again.PolicyDigest != policy.PolicyDigest {
		return empty, ErrUnavailable
	}
	var final mergeRequest
	if _, err = c.readJSON(ctx, token, path, &final); err != nil {
		return empty, err
	}
	if final.ID != mr.ID || final.IID != mr.IID || final.ProjectID != mr.ProjectID || final.TargetProjectID != mr.TargetProjectID || final.SourceProjectID != mr.SourceProjectID || final.TargetBranch != mr.TargetBranch || final.Imported || final.Draft || final.Author.ID != mr.Author.ID || final.SHA != mr.SHA || final.DiffRefs.Head != mr.SHA || final.State != "merged" || final.MergeSHA != mr.MergeSHA || final.SquashSHA != mr.SquashSHA || !final.MergedAt.Equal(mr.MergedAt) {
		return empty, ErrUnavailable
	}
	head, err := c.ResolveCommit(ctx, token, projectID, "refs/heads/"+branch)
	if err != nil || head != sha {
		return empty, ErrUnavailable
	}
	if _, err = c.VerifyRepository(ctx, token, repository, projectID); err != nil {
		return empty, err
	}
	evidence.Policy = again
	evidence.CheckedAt = time.Now().UTC()
	if !evidence.ValidForProvider("gitlab", 0, projectID, repository, branch, sha, evidence.CheckedAt, time.Minute) {
		return empty, fmt.Errorf("%w: incomplete reviewed merge evidence", ErrUnavailable)
	}
	return evidence, nil
}
