package hosting

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func DefaultRetentionRules() api.HostingRetentionRules {
	age := int64(api.HostingRetentionAgeDefault / time.Second)
	return api.HostingRetentionRules{PendingAgeSeconds: age, StagedAgeSeconds: age}
}
func ValidRetentionRules(r api.HostingRetentionRules) bool {
	min, max := int64(api.HostingRetentionAgeMin/time.Second), int64(api.HostingRetentionAgeMax/time.Second)
	return r.PendingAgeSeconds >= min && r.PendingAgeSeconds <= max && r.StagedAgeSeconds >= min && r.StagedAgeSeconds <= max
}

// CleanupPlan is a bounded catalog observation, never a deletion authorization.
// Callers must re-read every dependency under the site lock before retirement.
func CleanupPlan(site api.HostingSite, policy api.HostingRetentionPolicy, rules api.HostingRetentionRules, releases []api.HostingRelease, previews []api.HostingPreview, ciExpiry map[string]time.Time, now time.Time) api.HostingCleanupPlan {
	plan := api.HostingCleanupPlan{Policy: policy, Rules: rules, Proposed: rules != policy.HostingRetentionRules, EvaluatedAt: now, Entries: []api.HostingCleanupEntry{}}
	for _, release := range releases {
		entry := cleanupEntry(site, release, rules, previews, ciExpiry, now)
		plan.Entries = append(plan.Entries, entry)
		if entry.Eligible {
			plan.ReclaimableReservedBytes += entry.ReservedBytes
		}
		if release.State == "retiring" {
			plan.RetiringReservedBytes += entry.ReservedBytes
		}
	}
	return plan
}
func cleanupEntry(site api.HostingSite, r api.HostingRelease, rules api.HostingRetentionRules, previews []api.HostingPreview, ciExpiry map[string]time.Time, now time.Time) api.HostingCleanupEntry {
	e := api.HostingCleanupEntry{ReleaseID: r.ID, State: r.State, ReservedBytes: api.HostingStorageCopies*r.ArtifactBytes + api.HostingManifestMaxBytes}
	switch {
	case site.Status != "active" || !site.DurableRouting:
		e.Reason = "site_ineligible"
		return e
	case r.State == "retiring":
		e.Reason = "awaiting_artifact_cleanup"
		if r.RetirementPublishedAt == nil {
			e.Reason = "awaiting_retirement_publication"
		} else if now.Sub(*r.RetirementPublishedAt) < api.HostingRetirementGrace {
			e.Reason = "retirement_grace_period"
		}
		return e
	case r.ID == site.ActiveReleaseID:
		e.Reason = "active_release"
		return e
	case r.FirstPromotedAt != nil:
		e.Reason = "previously_published"
		return e
	}
	for _, p := range previews {
		if p.ReleaseID == r.ID {
			e.Reason = "preview_cleanup_pending"
			if !p.Revoked && p.ExpiresAt.After(now) {
				e.Reason = "active_preview"
				return e
			}
		}
	}
	if e.Reason != "" {
		return e
	}
	expiry, ci := ciExpiry[r.ID]
	if ci && expiry.After(now) {
		e.Reason = "ci_retry_window"
		return e
	}
	if r.State != "pending" && (r.State != "ready" || !ci) {
		e.Reason = "manual_staging"
		return e
	}
	if r.LastActivityAt.IsZero() || r.CreatedAt.IsZero() {
		e.Reason = "activity_unknown"
		return e
	}
	last := r.LastActivityAt
	if r.CreatedAt.After(last) {
		last = r.CreatedAt
	}
	age := rules.PendingAgeSeconds
	if r.State == "ready" {
		age = rules.StagedAgeSeconds
	}
	at := last.Add(time.Duration(age) * time.Second)
	if ci && expiry.After(at) {
		at = expiry
	}
	e.EligibleAt = &at
	if now.Before(at) {
		e.Reason = "recent_activity"
		return e
	}
	e.Eligible = true
	e.Reason = "abandoned_upload"
	if r.State == "ready" {
		e.Reason = "expired_ci_release"
	}
	return e
}
