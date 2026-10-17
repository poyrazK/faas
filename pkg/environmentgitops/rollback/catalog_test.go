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

func TestRecoveryCatalogRejectsUnbackedRetentionClaims(t *testing.T) {
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	out := api.ListEnvironmentGitReleasesResponse{Project: "shop", Environment: "production", SourceID: "source", TargetCommitSHA: strings.Repeat("a", 40), ObservedAt: now, ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true,
		RecoveryRetention: &api.EnvironmentRecoveryRetention{Releases: 3, Seconds: 3600, MaxBytes: 1000}, RetentionBytes: 100,
		Releases: []api.EnvironmentGitRecoveryRelease{{ReleaseSetID: "2ba0aa40-2362-4c0e-aeb1-6381666bb514", CreatedAt: now, RetentionStatus: "active", Eligibility: "blocked", BlockingReasons: []string{"trust_revoked"}, ProtectionStatus: "protected", ArtifactsProtectedUntil: &until, RetainedBytes: 100}}}
	if err := ValidateCatalog(out); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*api.ListEnvironmentGitReleasesResponse){
		func(o *api.ListEnvironmentGitReleasesResponse) { o.RecoveryRetention = nil },
		func(o *api.ListEnvironmentGitReleasesResponse) { o.RetentionBytes = 1 },
		func(o *api.ListEnvironmentGitReleasesResponse) { o.Releases[0].ArtifactsProtectedUntil = &now },
		func(o *api.ListEnvironmentGitReleasesResponse) { o.Releases[0].ProtectionStatus = "expired" },
	} {
		copied := out
		copied.Releases = append([]api.EnvironmentGitRecoveryRelease(nil), out.Releases...)
		mutate(&copied)
		if ValidateCatalog(copied) == nil {
			t.Fatal("unbacked protection accepted", copied)
		}
	}
}
