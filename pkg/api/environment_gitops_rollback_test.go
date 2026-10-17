package api

import (
	"strings"
	"testing"
)

func TestRollbackRequestRequiresCanonicalSelection(t *testing.T) {
	valid := PreviewEnvironmentGitRollbackRequest{ReleaseSetID: "2ba0aa40-2362-4c0e-aeb1-6381666bb514", TargetCommitSHA: strings.Repeat("a", 40), ExpectedPlanHash: strings.Repeat("b", 64)}
	if valid.Validate() != nil {
		t.Fatal("valid request rejected")
	}
	for _, change := range []func(*PreviewEnvironmentGitRollbackRequest){
		func(r *PreviewEnvironmentGitRollbackRequest) { r.ReleaseSetID = "foreign" },
		func(r *PreviewEnvironmentGitRollbackRequest) {
			r.ReleaseSetID = strings.ReplaceAll(r.ReleaseSetID, "-", "")
		},
		func(r *PreviewEnvironmentGitRollbackRequest) { r.TargetCommitSHA = "main" },
		func(r *PreviewEnvironmentGitRollbackRequest) { r.TargetCommitSHA = strings.Repeat("A", 40) },
		func(r *PreviewEnvironmentGitRollbackRequest) { r.ExpectedPlanHash = "old" },
	} {
		r := valid
		change(&r)
		if r.Validate() == nil {
			t.Fatalf("invalid selection accepted: %+v", r)
		}
	}
}
