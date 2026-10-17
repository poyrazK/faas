package rollback

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRecoveryCatalogRejectsUnprovenEligibilityAndUnscopedPages(t *testing.T) {
	now := time.Now().UTC()
	valid := func() api.ListEnvironmentGitReleasesResponse {
		return api.ListEnvironmentGitReleasesResponse{Project: "shop", Environment: "production", SourceID: "source", TargetCommitSHA: strings.Repeat("a", 40), ObservedAt: now, ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true, Releases: []api.EnvironmentGitRecoveryRelease{{ReleaseSetID: "2ba0aa40-2362-4c0e-aeb1-6381666bb514", CreatedAt: now, ServedAt: &now, CommitSHA: strings.Repeat("b", 40), PlanHash: strings.Repeat("c", 64), Eligibility: "eligible", RetentionStatus: "active"}}}
	}
	if err := ValidateCatalog(valid()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*api.ListEnvironmentGitReleasesResponse){
		func(out *api.ListEnvironmentGitReleasesResponse) { out.ReadOnly = false },
		func(out *api.ListEnvironmentGitReleasesResponse) { out.ValuesRedacted = false },
		func(out *api.ListEnvironmentGitReleasesResponse) { out.RequiresTargetQualification = false },
		func(out *api.ListEnvironmentGitReleasesResponse) { out.Releases[0].ServedAt = nil },
		func(out *api.ListEnvironmentGitReleasesResponse) { out.Releases[0].PlanHash = "" },
		func(out *api.ListEnvironmentGitReleasesResponse) { out.Releases[0].PlanHash = strings.Repeat("z", 64) },
		func(out *api.ListEnvironmentGitReleasesResponse) {
			out.Releases[0].BlockingReasons = []string{"missing artifact"}
		},
		func(out *api.ListEnvironmentGitReleasesResponse) { out.NextBefore = "foreign" },
		func(out *api.ListEnvironmentGitReleasesResponse) {
			out.Releases = append(out.Releases, out.Releases[0])
		},
	} {
		out := valid()
		mutate(&out)
		if ValidateCatalog(out) == nil {
			t.Fatalf("unsafe catalog accepted: %+v", out)
		}
	}
}
