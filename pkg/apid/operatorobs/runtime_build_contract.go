package operatorobs

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeBuildsValid keeps report fields on a closed, bounded read contract.
// Unknown response fields never become part of saved deployment evidence.
func RuntimeBuildsValid(out api.ObsRuntimeBuilds) bool {
	if out.GeneratedAt.IsZero() || out.EvidenceAfter.IsZero() || out.Scope != RuntimeBuildsScope || !runtimeRevisionPattern.MatchString(out.RequestedRelease) || out.MaxAgeSeconds != int(api.ObsInputHealthMaxAge.Seconds()) || !slices.Equal(out.Limitations, RuntimeBuildLimitations()) || len(out.Items) > api.ObsRuntimeBuildPreviewMax || out.OmittedItems < 0 {
		return false
	}
	specs := runtimeBuildJobSpecs(out.ExpectedRole)
	if len(specs) == 0 {
		return out.ExpectedRole == "" && out.State == "unknown" && out.Reason == "expected_role_not_selected" && len(out.Jobs) == 0 && len(out.Items) == 0 && out.Counts == (api.ObsRuntimeBuildCounts{}) && out.OmittedItems == 0
	}
	if len(out.Jobs) != len(specs) || len(specs) > api.ObsRuntimeBuildJobsMax {
		return false
	}
	expected, incomplete := 0, false
	for i, job := range out.Jobs {
		spec := specs[i]
		if job.Job != spec.job || job.Daemon != spec.daemon || job.Expected < 0 || job.Expected > api.ObsRuntimeBuildNodesMax || !slices.Contains([]string{"available", "unknown"}, job.State) || !slices.Contains([]string{"", "inventory_unavailable", "source_unavailable", "invalid_evidence"}, job.Reason) || (job.State == "available") != (job.Reason == "") {
			return false
		}
		if spec.discovery {
			if job.InventorySource != "active_configured_compute_registry" {
				return false
			}
		} else if job.InventorySource != "scrape_targets_minimum" || job.Expected != 1 {
			return false
		}
		expected += job.Expected
		incomplete = incomplete || job.State != "available"
	}
	c := out.Counts
	for _, count := range []int{c.Expected, c.Observed, c.Matching, c.Mismatched, c.Missing, c.Stale, c.Unavailable, c.Unexpected, c.DistinctRevisions, out.OmittedItems} {
		if count < 0 || count > len(specs)*(api.ObsRuntimeBuildSamplesMax+api.ObsRuntimeBuildNodesMax) {
			return false
		}
	}
	if c.Expected != expected || c.Matching+c.Mismatched+c.Missing+c.Stale+c.Unavailable != len(out.Items)+out.OmittedItems || c.Matching+c.Mismatched > c.Observed || c.Unexpected > c.Observed || c.DistinctRevisions > c.Matching+c.Mismatched || (c.DistinctRevisions == 0) != (c.Matching+c.Mismatched == 0) {
		return false
	}
	state, reason := runtimeBuildState(c, incomplete)
	if out.State != state || out.Reason != reason {
		return false
	}
	for _, item := range out.Items {
		if !runtimeBuildItemValid(item, specs, out) {
			return false
		}
	}
	body, err := json.MarshalIndent(out, "", "  ")
	return err == nil && len(body) < api.ObsRuntimeBuildResponseMaxBytes
}

func runtimeBuildItemValid(item api.ObsRuntimeBuildItem, specs []runtimeBuildJobSpec, out api.ObsRuntimeBuilds) bool {
	known := slices.ContainsFunc(specs, func(spec runtimeBuildJobSpec) bool { return spec.job == item.Job && spec.daemon == item.Daemon })
	if !known || item.Node != "" && !ServingProbeNodeValid(item.Node) || item.TargetID != "" && !runtimeTargetIDValid(item.TargetID) || len(item.Version) > api.ObsRuntimeBuildVersionMaxBytes || item.Version != "" && !runtimeVersionPattern.MatchString(item.Version) {
		return false
	}
	for _, at := range []*time.Time{item.ObservedAt, item.ScrapeObservedAt} {
		if at != nil && (at.IsZero() || at.After(out.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
			return false
		}
	}
	switch item.State {
	case "matching", "mismatched":
		for _, at := range []*time.Time{item.ObservedAt, item.ScrapeObservedAt} {
			if at == nil || at.Before(out.GeneratedAt.Add(-api.ObsInputHealthMaxAge)) || at.Before(out.EvidenceAfter.Add(-api.ObsHealthClockSkewTolerance)) {
				return false
			}
		}
		return runtimeRevisionPattern.MatchString(item.GitSHA) && (item.State == "matching") == (item.GitSHA == out.RequestedRelease) && item.TargetID != "" && item.ObservedAt != nil && item.ScrapeObservedAt != nil && (item.State == "matching" && item.Reason == "" || item.State == "mismatched" && item.Reason == "requested_revision_mismatch")
	case "missing", "stale", "unavailable":
		return item.GitSHA == "" && item.Version == "" && slices.Contains([]string{"source_unavailable", "invalid_evidence", "conflicting_targets", "expected_target_missing", "invalid_sample_time", "scrape_stale", "scrape_before_boundary", "scrape_down", "build_info_missing", "conflicting_build_info", "build_info_stale", "build_info_before_boundary", "revision_unavailable"}, item.Reason)
	default:
		return false
	}
}

func runtimeTargetIDValid(value string) bool {
	// The public target identity is the complete SHA-256 digest, never an address.
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}
