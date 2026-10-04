package gitapproval

import (
	"strings"
	"testing"
	"time"
)

func mergeFixture(now time.Time) MergeEvidence {
	return MergeEvidence{Qualified: true, Profile: ReviewedMergeProfile,
		Policy: PolicyEvidence{Qualified: true, Profile: ProtectedBranchProfile, InstallationID: 42, RepositoryID: 123,
			Repository: "octo/api", Branch: "main", CommitSHA: strings.Repeat("a", 40), PolicyDigest: strings.Repeat("b", 64), RequiredReviewCount: 1, CheckedAt: now},
		PullRequestID: 1234, PullRequestNumber: 7, AuthorID: 10, HeadSHA: strings.Repeat("c", 40), MergedAt: now.Add(-time.Hour), CheckedAt: now,
		Reviews: []ReviewEvidence{{ID: 1, ReviewerID: 11, Reviewer: "reviewer", HeadSHA: strings.Repeat("c", 40), SubmittedAt: now.Add(-2 * time.Hour)}}}
}

func TestReviewedMergeEvidenceRequiresExactFreshHumanReviewReceipt(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		mutate func(*MergeEvidence)
	}{
		{"other repository", func(e *MergeEvidence) { e.Policy.RepositoryID++ }},
		{"other branch", func(e *MergeEvidence) { e.Policy.Branch = "staging" }},
		{"other commit", func(e *MergeEvidence) { e.Policy.CommitSHA = strings.Repeat("f", 40) }},
		{"unqualified", func(e *MergeEvidence) { e.Qualified = false }},
		{"unknown profile", func(e *MergeEvidence) { e.Profile = "unknown" }},
		{"missing PR", func(e *MergeEvidence) { e.PullRequestID = 0 }},
		{"old head review", func(e *MergeEvidence) { e.Reviews[0].HeadSHA = strings.Repeat("d", 40) }},
		{"author approval", func(e *MergeEvidence) { e.Reviews[0].ReviewerID = e.AuthorID }},
		{"duplicate reviewer", func(e *MergeEvidence) {
			r := e.Reviews[0]
			r.ID++
			e.Reviews = append(e.Reviews, r)
			e.Policy.RequiredReviewCount = 2
		}},
		{"duplicate review", func(e *MergeEvidence) {
			r := e.Reviews[0]
			r.ReviewerID++
			e.Reviews = append(e.Reviews, r)
			e.Policy.RequiredReviewCount = 2
		}},
		{"insufficient reviews", func(e *MergeEvidence) { e.Policy.RequiredReviewCount = 2 }},
		{"no review requirement", func(e *MergeEvidence) { e.Policy.RequiredReviewCount = 0 }},
		{"after merge", func(e *MergeEvidence) { e.Reviews[0].SubmittedAt = e.MergedAt.Add(time.Second) }},
		{"ambiguous merge order", func(e *MergeEvidence) { e.Reviews[0].SubmittedAt = e.MergedAt }},
		{"stale policy", func(e *MergeEvidence) { e.Policy.CheckedAt = now.Add(-2 * time.Minute) }},
		{"stale proof", func(e *MergeEvidence) { e.CheckedAt = now.Add(-2 * time.Minute) }},
		{"future proof", func(e *MergeEvidence) { e.CheckedAt = now.Add(time.Second) }},
		{"future policy", func(e *MergeEvidence) { e.Policy.CheckedAt = now.Add(time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := mergeFixture(now)
			tc.mutate(&e)
			if e.ValidFor(42, 123, "octo/api", "main", strings.Repeat("a", 40), now, time.Minute) {
				t.Fatal("invalid evidence qualified")
			}
		})
	}
	if !mergeFixture(now).ValidFor(42, 123, "octo/api", "main", strings.Repeat("a", 40), now, time.Minute) {
		t.Fatal("valid receipt rejected")
	}
}
