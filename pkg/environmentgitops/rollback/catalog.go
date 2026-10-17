package rollback

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
)

// ValidateCatalog keeps CLI responses scoped and never interprets eligibility as readiness.
func ValidateCatalog(out api.ListEnvironmentGitReleasesResponse) error {
	if !out.ReadOnly || !out.ValuesRedacted || !out.RequiresTargetQualification || out.SourceID == "" || out.ObservedAt.IsZero() || !api.ValidProjectSlug(out.Project) || !api.ValidProjectEnvironmentSlug(out.Environment) || (api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: out.TargetCommitSHA, Before: out.NextBefore}).Validate() != nil || len(out.Releases) > api.EnvironmentGitRecoveryPageSize {
		return fmt.Errorf("invalid recovery catalog scope or preview boundary")
	}

	if out.RetentionBytes < 0 || (out.RecoveryRetention == nil && out.RetentionBytes != 0) || out.RecoveryRetention != nil && (out.RecoveryRetention.Validate() != nil || out.RetentionBytes > out.RecoveryRetention.MaxBytes) {
		return fmt.Errorf("invalid recovery retention accounting")
	}
	var pageBytes int64
	seen := map[string]bool{}
	for _, release := range out.Releases {
		id, err := uuid.Parse(release.ReleaseSetID)
		if err != nil || id.String() != release.ReleaseSetID || seen[release.ReleaseSetID] || release.CreatedAt.IsZero() || !slices.Contains([]string{"active", "unknown", "compatibility_window", "compatibility_window_elapsed"}, release.RetentionStatus) || (release.Eligibility != "eligible" && release.Eligibility != "blocked") || (release.Eligibility == "eligible") != (len(release.BlockingReasons) == 0) {
			return fmt.Errorf("invalid recovery release")
		}

		if release.ProtectionStatus != "" && !slices.Contains([]string{"disabled", "expired", "outside_policy", "budget_exceeded", "protected"}, release.ProtectionStatus) {
			return fmt.Errorf("invalid artifact protection status")
		}
		if release.RetainedBytes < 0 || (release.ProtectionStatus == "protected") != (release.ArtifactsProtectedUntil != nil) || release.ProtectionStatus != "protected" && release.RetainedBytes != 0 {
			return fmt.Errorf("invalid artifact protection proof")
		}
		if release.ArtifactsProtectedUntil != nil && (out.RecoveryRetention == nil || !release.ArtifactsProtectedUntil.After(out.ObservedAt) || release.RetainedBytes <= 0 || release.RetainedBytes > out.RetentionBytes) {
			return fmt.Errorf("invalid artifact protection window")
		}
		pageBytes += release.RetainedBytes
		seen[release.ReleaseSetID] = true
		digest, digestErr := hex.DecodeString(release.PlanHash)
		if release.Eligibility == "eligible" && (release.ServedAt == nil || release.ServedAt.IsZero() || (api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: release.CommitSHA}).Validate() != nil || digestErr != nil || len(digest) != 32 || strings.ToLower(release.PlanHash) != release.PlanHash) {
			return fmt.Errorf("unproven recovery eligibility")
		}
		for _, artifact := range release.Artifacts {
			if promotion.HasBuildArtifact(artifact) && !promotion.ValidBuildArtifact(artifact) || artifact.ImageDigest != "" && !promotion.ValidImageIdentity(artifact.ImageDigest) {
				return fmt.Errorf("invalid recovery artifact identity")
			}
		}
	}
	if pageBytes > out.RetentionBytes {
		return fmt.Errorf("recovery page exceeds reported retention usage")
	}
	if out.NextBefore != "" && (len(out.Releases) == 0 || out.NextBefore != out.Releases[len(out.Releases)-1].ReleaseSetID) {
		return fmt.Errorf("invalid recovery page boundary")
	}
	return nil
}
