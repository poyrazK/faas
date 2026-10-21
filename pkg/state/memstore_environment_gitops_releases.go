package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsRecoveryStore = (*MemStore)(nil)

func (m *MemStore) ListEnvironmentGitReleases(ctx context.Context, source EnvironmentGitSource, request api.ListEnvironmentGitReleasesRequest, desired environmentsync.DesiredState) (api.ListEnvironmentGitReleasesResponse, error) {
	out, err := recoveryListing(source, request, desired, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory := m.environmentGitOps[source.ID]
	if memory == nil || memory.source.AccountID != source.AccountID {
		return out, ErrNotFound
	}
	if !promotionSourceMatches(memory.source, source) {
		return out, ErrConflict
	}
	out.ObservedAt = time.Now().UTC()
	out.RecoveryRetention = recoveryPolicy(memory)
	protection := m.recoveryProtectionLocked(memory, out.RecoveryRetention, out.ObservedAt)
	for _, row := range protection {
		if row.status == "protected" {
			out.RetentionBytes += row.bytes
		}
	}
	releases := []ProjectReleaseSet{}
	for _, release := range m.projectReleaseSets {
		if release.AccountID != source.AccountID || release.ProjectID != source.ProjectID || release.EnvironmentSlug != source.EnvironmentSlug {
			continue
		}
		for _, graph := range memory.graphs {
			receipt, exists := m.environmentWorkloadServingReceipts[graph.ID]
			if exists && receipt.ReleaseSetID == release.ID && historicalServingAt(receipt) != nil && graph.EnvironmentID == source.EnvironmentID {
				releases = append(releases, release)
				break
			}
		}
	}
	slices.SortFunc(releases, func(a, b ProjectReleaseSet) int {
		if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(b.ID, a.ID)
	})
	if request.Before != "" {
		index := slices.IndexFunc(releases, func(release ProjectReleaseSet) bool { return release.ID == request.Before })
		if index < 0 {
			return out, ErrNotFound
		}
		releases = releases[index+1:]
	}
	if len(releases) > api.EnvironmentGitRecoveryPageSize {
		releases = releases[:api.EnvironmentGitRecoveryPageSize]
		out.NextBefore = releases[len(releases)-1].ID
	}
	for _, release := range releases {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		plan, err := m.previewEnvironmentGitOpsRollbackLocked(memory, api.PreviewEnvironmentGitRollbackRequest{ReleaseSetID: release.ID, TargetCommitSHA: request.TargetCommitSHA}, desired, out.ObservedAt)
		entry, err := recoveryRelease(release, plan, err, out.ObservedAt)
		if err != nil {
			return out, err
		}
		if out.RecoveryRetention != nil {
			entry.ProtectionStatus = "expired"
		}

		for _, g := range memory.graphs {
			receipt := m.environmentWorkloadServingReceipts[g.ID]
			if receipt.ReleaseSetID == release.ID && g.EnvironmentID == source.EnvironmentID && historicalServingAt(receipt) != nil {
				first := *historicalServingAt(receipt)
				if entry.ServedAt == nil || first.Before(*entry.ServedAt) {
					entry.ServedAt = &first
				}
			}
		}
		applyRecoveryProtection(&entry, protection)
		out.Releases = append(out.Releases, entry)
	}
	return out, nil
}
