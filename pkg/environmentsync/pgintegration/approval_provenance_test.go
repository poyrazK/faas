package pgintegration_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/state"
)

func reviewedMergeFixture(source state.EnvironmentGitSource, sha string) gitapproval.MergeEvidence {
	now := time.Now().UTC()
	head := strings.Repeat("b", 40)
	return gitapproval.MergeEvidence{Qualified: true, Profile: gitapproval.ReviewedMergeProfile,
		Policy: gitapproval.PolicyEvidence{Qualified: true, Profile: gitapproval.ProtectedBranchProfile, InstallationID: source.Spec.InstallationID, RepositoryID: source.Spec.RepositoryID,
			Repository: source.Spec.Repository, Branch: "main", CommitSHA: sha, PolicyDigest: strings.Repeat("c", 64), RequiredReviewCount: 1, CheckedAt: now},
		PullRequestID: 1234, PullRequestNumber: 7, AuthorID: 10, HeadSHA: head, MergedAt: now.Add(-time.Hour), CheckedAt: now,
		Reviews: []gitapproval.ReviewEvidence{{ID: 1, ReviewerID: 11, Reviewer: "reviewer", HeadSHA: head, SubmittedAt: now.Add(-2 * time.Hour)}}}
}

func TestEnvironmentGitReviewedMergeApprovalIsAtomicAndRetainsOriginalProvenance(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seedModePolicy(t, store, "report", "protected_branch")
		sha := strings.Repeat("a", 40)
		if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, sha)); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("manual bypass: %v", err)
		}
		polls := store.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		evidence := reviewedMergeFixture(source, sha)
		evidence.ReviewedDefinitionDigest = desired.Digest
		result := state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest, Desired: &desired, Approval: &evidence}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		approved, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || approved.Generation != 1 || approved.ApprovedRevisionID == "" || approved.SourceCommitSHA != sha || approved.SourceDefinitionDigest != desired.Digest || approved.AppliedRevisionID != "" {
			t.Fatalf("atomic approval: %+v %v", approved, err)
		}
		approvals := store.(state.EnvironmentGitApprovalStore)
		record, err := approvals.EnvironmentGitRevisionApproval(t.Context(), source.AccountID, source.ID, approved.ApprovedRevisionID)
		if err != nil || record.Generation != approved.Generation || record.DefinitionDigest != desired.Digest || record.Evidence.PullRequestID != 1234 || record.Evidence.Policy.CommitSHA != sha {
			t.Fatalf("provenance: %+v %v", record, err)
		}
		if _, err := approvals.EnvironmentGitRevisionApproval(t.Context(), uuid.NewString(), source.ID, approved.ApprovedRevisionID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign approval read: %v", err)
		}
		job, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
		if err != nil || job.Revision.ID != approved.ApprovedRevisionID || job.Revision.Digest != desired.Digest || job.Revision.ApprovedBy != "github:protected_branch" {
			t.Fatalf("work not bound to approval: %+v %v", job, err)
		}
		next := now.Add(time.Minute)
		retry, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), next, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		evidence = reviewedMergeFixture(approved, sha)
		evidence.ReviewedDefinitionDigest = desired.Digest
		evidence.PullRequestID = 9999
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), retry, result, next, next.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		latest, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		retained, err := approvals.EnvironmentGitRevisionApproval(t.Context(), source.AccountID, source.ID, approved.ApprovedRevisionID)
		if err != nil || retained.ID != record.ID || retained.Evidence.PullRequestID != 1234 || latest.Generation != approved.Generation {
			t.Fatalf("retry replaced original provenance: %+v %v", retained, err)
		}
		if err := store.RenewEnvironmentGitOps(t.Context(), job, time.Now(), time.Minute); err != nil {
			t.Fatalf("identical approval revoked running work: %v", err)
		}
		outageAt := next.Add(time.Minute)
		failed, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), outageAt, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), failed, state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_unavailable"}, outageAt, outageAt.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		latest, err = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || latest.ApprovedRevisionID != approved.ApprovedRevisionID || latest.SourceCommitSHA != sha || latest.Generation != approved.Generation || latest.SourceErrorCode != "environment_git_approval_unavailable" {
			t.Fatalf("approval outage discarded approved state: %+v %v", latest, err)
		}
	})
}

func TestEnvironmentGitReviewedMergeApprovalRejectsInvalidProofWithoutConsumingLease(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*state.EnvironmentGitSourcePollResult)
	}{
		{"missing evidence", func(r *state.EnvironmentGitSourcePollResult) { r.Desired = nil; r.Approval = nil }},
		{"unbound reviewed definition", func(r *state.EnvironmentGitSourcePollResult) { r.Approval.ReviewedDefinitionDigest = "" }},
		{"different repository", func(r *state.EnvironmentGitSourcePollResult) { r.Approval.Policy.RepositoryID++ }},
		{"different head", func(r *state.EnvironmentGitSourcePollResult) { r.Approval.Reviews[0].HeadSHA = strings.Repeat("d", 40) }},
		{"expired evidence", func(r *state.EnvironmentGitSourcePollResult) {
			r.Approval.Policy.CheckedAt = time.Now().Add(-2 * time.Minute)
		}},
		{"future evidence", func(r *state.EnvironmentGitSourcePollResult) { r.Approval.CheckedAt = time.Now().Add(time.Hour) }},
		{"wrong definition", func(r *state.EnvironmentGitSourcePollResult) { r.Desired.Definition.Environment = "staging" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stores(t, func(t *testing.T, store gitOpsTestStore) {
				source, desired := seedModePolicy(t, store, "report", "protected_branch")
				polls := store.(state.EnvironmentGitSourcePollStore)
				now := time.Now().UTC()
				lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				sha := strings.Repeat("a", 40)
				evidence := reviewedMergeFixture(source, sha)
				evidence.ReviewedDefinitionDigest = desired.Digest
				invalid := desired
				result := state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest, Desired: &invalid, Approval: &evidence}
				tc.mutate(&result)
				if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, now, now.Add(time.Minute)); !errors.Is(err, state.ErrInvalidArgument) {
					t.Fatalf("invalid proof accepted: %v", err)
				}
				current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
				if err != nil || current.Generation != 0 || current.ApprovedRevisionID != "" || current.SourceCommitSHA != "" {
					t.Fatalf("invalid proof wrote authority: %+v %v", current, err)
				}
				if _, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), now, time.Minute); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("invalid proof scheduled work: %v", err)
				}
				evidence = reviewedMergeFixture(source, sha)
				evidence.ReviewedDefinitionDigest = desired.Digest
				result = state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest, Desired: &desired, Approval: &evidence}
				if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, result, now, now.Add(time.Minute)); err != nil {
					t.Fatalf("rejection consumed lease: %v", err)
				}
			})
		})
	}
}

func TestEnvironmentGitReviewedMergeApprovalRejectsReplacedLease(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seedModePolicy(t, store, "report", "protected_branch")
		polls := store.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		old, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		recoveredAt := now.Add(time.Minute)
		current, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), recoveredAt, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		evidence := reviewedMergeFixture(source, strings.Repeat("a", 40))
		evidence.ReviewedDefinitionDigest = desired.Digest
		result := state.EnvironmentGitSourcePollResult{CommitSHA: evidence.Policy.CommitSHA, Digest: desired.Digest, Desired: &desired, Approval: &evidence}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), old, result, recoveredAt, recoveredAt.Add(time.Minute)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old lease granted approval: %v", err)
		}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), current, result, recoveredAt, recoveredAt.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	})
}
