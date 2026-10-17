package hosting

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostingCleanupProtectsServingAndRetryDependencies(t *testing.T) {
	// adr: 202610171001 — automatic retirement never reaches published or referenced releases.
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	site := api.HostingSite{ID: "site", Status: "active", DurableRouting: true}
	policy := api.HostingRetentionPolicy{SiteID: site.ID, HostingRetentionRules: DefaultRetentionRules()}
	for _, tt := range []struct {
		name, state, reason   string
		active, published, ci bool
		expiry                time.Time
		preview               *api.HostingPreview
		last                  time.Time
		eligible              bool
	}{
		{name: "abandoned manual upload", state: "pending", reason: "abandoned_upload", last: old, eligible: true},
		{name: "manual ready", state: "ready", reason: "manual_staging", last: old},
		{name: "published", state: "ready", reason: "previously_published", published: true, last: old, ci: true},
		{name: "active", state: "ready", reason: "active_release", active: true, last: old, ci: true},
		{name: "recent upload", state: "pending", reason: "recent_activity", last: now.Add(-time.Minute)},
		{name: "unknown activity", state: "pending", reason: "activity_unknown"},
		{name: "CI retry", state: "pending", reason: "ci_retry_window", ci: true, expiry: now.Add(time.Hour), last: old},
		{name: "expired CI ready", state: "ready", reason: "expired_ci_release", ci: true, expiry: old, last: old, eligible: true},
		{name: "live preview", state: "ready", reason: "active_preview", ci: true, last: old, preview: &api.HostingPreview{ReleaseID: "release", ExpiresAt: now.Add(time.Hour)}},
		{name: "expired preview row", state: "ready", reason: "preview_cleanup_pending", ci: true, last: old, preview: &api.HostingPreview{ReleaseID: "release", ExpiresAt: old}},
		{name: "revoked preview row", state: "ready", reason: "preview_cleanup_pending", ci: true, last: old, preview: &api.HostingPreview{ReleaseID: "release", ExpiresAt: old, Revoked: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			current := site
			r := api.HostingRelease{ID: "release", SiteID: site.ID, State: tt.state, CreatedAt: old, LastActivityAt: tt.last, ArtifactBytes: 18}
			if tt.active {
				current.ActiveReleaseID = r.ID
			}
			if tt.published {
				r.FirstPromotedAt = &old
			}
			previews := []api.HostingPreview{}
			if tt.preview != nil {
				previews = append(previews, *tt.preview)
			}
			expiry := map[string]time.Time{}
			if tt.ci {
				expiry[r.ID] = tt.expiry
			}
			plan := CleanupPlan(current, policy, policy.HostingRetentionRules, []api.HostingRelease{r}, previews, expiry, now)
			entry := plan.Entries[0]
			if entry.Eligible != tt.eligible || entry.Reason != tt.reason {
				t.Fatal(entry)
			}
			expected := int64(0)
			if tt.eligible {
				expected = 2*r.ArtifactBytes + api.HostingManifestMaxBytes
			}
			if plan.ReclaimableReservedBytes != expected || plan.Policy.Enabled {
				t.Fatal("plan mutates policy or misstates reservation", plan)
			}
		})
	}
}

func TestHostingRetentionIdleMinimumCoversInFlightWorkflow(t *testing.T) {
	if api.HostingRetentionAgeMin <= api.HostingCIWorkflowTimeout || api.HostingRetentionAgeMin <= api.HostingUploadTTL+api.HostingFinalizeTimeout+api.HostingStorageTimeout {
		t.Fatal("retention can expire bounded in-flight work")
	}
}
