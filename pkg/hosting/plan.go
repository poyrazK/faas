package hosting

import (
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DeploymentPlanEvidence consists only of successful, operator-scoped reads.
// These independent reads cannot provide a transactionally consistent snapshot.
type DeploymentPlanEvidence struct {
	Site         api.HostingSite
	Capabilities api.HostingCapabilities
	Releases     []api.HostingRelease
	Usage        api.HostingUsage
}

func PlanDeployment(e DeploymentPlanEvidence, local api.HostingManifest, started, now time.Time) (api.HostingDeploymentPlan, error) {
	out := api.HostingDeploymentPlan{}
	if err := validatePlanEvidence(e); err != nil {
		return out, err
	}
	data, err := json.Marshal(local)
	if err != nil || int64(len(data)) > api.HostingManifestMaxBytes {
		return out, errors.New("local manifest exceeds its serialized byte limit")
	}
	baseline, err := planBaseline(e.Site, e.Releases)
	if err != nil {
		return out, err
	}
	diff, err := compareManifests(baseline, local)
	if err != nil {
		return out, err
	}
	out = api.HostingDeploymentPlan{Version: api.HostingDeploymentPlanVersion, SiteID: e.Site.ID, ExpectedRevision: e.Site.Revision, BaseReleaseID: e.Site.ActiveReleaseID,
		ManifestFingerprint: HostingManifestFingerprint(local), StartedAt: started, ObservedAt: now, UsageObservedAt: e.Usage.ObservedAt,
		Advisory: true, State: "within_observed_limits", FileCount: len(local.Files), PayloadBytes: diff.AfterBytes, ManifestBytes: int64(len(data)),
		AdditionalReservedBytes: api.HostingStorageCopies*diff.AfterBytes + api.HostingManifestMaxBytes, Changes: diff,
		Quotas: []api.HostingPlanQuota{}, Blockers: []string{}, Warnings: []string{"Independent reads are advisory. Deployment must revalidate capacity and site revision."}}
	if err := addPlanQuotas(&out, e); err != nil {
		return api.HostingDeploymentPlan{}, err
	}
	if local.Config != nil && !e.Capabilities.ConfigEnabled {
		out.Blockers = append(out.Blockers, "config_disabled")
	}
	if e.Site.Suspended {
		out.Warnings = append(out.Warnings, "Site traffic is suspended. Deploying does not resume it.")
	}
	if len(out.Blockers) > 0 {
		out.State = "blocked"
	}
	return out, nil
}

func validatePlanEvidence(e DeploymentPlanEvidence) error {
	c, u, s := e.Capabilities, e.Usage, e.Site
	if !ValidCIRunID(s.ID) || !ValidCIRunID(s.AccountID) || s.Status != "active" || s.Revision < 1 || u.AccountID != s.AccountID || u.ObservedAt.IsZero() || c.AutomationSiteID != "" {
		return errors.New("deployment planning requires matching operator account usage and an active site")
	}
	if c.FilesMax != api.HostingFilesMax || c.FileMaxBytes != api.HostingFileMaxBytes || c.ReleaseMaxBytes != api.HostingReleaseMaxBytes || c.ManifestMaxBytes != api.HostingManifestMaxBytes || c.ReleasesPerSite != api.HostingReleasesPerSite || c.AccountStorageMaxBytes != api.HostingAccountStorageMaxBytes || c.AccountPendingUploadsMax != api.HostingAccountPendingUploadsMax || u.StorageLimitBytes != c.AccountStorageMaxBytes || u.PendingUploadsLimit != c.AccountPendingUploadsMax {
		return errors.New("deployment planning limits differ from this CLI; upgrade before planning")
	}
	if u.ReservedBytes < 0 || u.PendingUploads < 0 || u.Releases < int64(len(e.Releases)) || u.PendingUploads > u.Releases {
		return errors.New("deployment planning usage is invalid; refresh before planning")
	}
	return nil
}

func planBaseline(site api.HostingSite, releases []api.HostingRelease) (*api.HostingManifest, error) {
	var baseline *api.HostingManifest
	seen := map[string]bool{}
	for _, r := range releases {
		if !ValidCIRunID(r.ID) || r.SiteID != site.ID || seen[r.ID] || r.State != "pending" && r.State != "ready" && r.State != "retiring" {
			return nil, errors.New("release inventory is invalid; refresh before planning")
		}
		seen[r.ID] = true
		if r.ID == site.ActiveReleaseID {
			if r.State != "ready" {
				return nil, errors.New("active release is not verified; refresh before planning")
			}
			baseline = &r.Manifest
		}
	}
	if site.ActiveReleaseID != "" && baseline == nil {
		return nil, errors.New("active release baseline is missing; refresh before planning")
	}
	return baseline, nil
}

func addPlanQuotas(out *api.HostingDeploymentPlan, e DeploymentPlanEvidence) error {
	checks := []api.HostingPlanQuota{
		{Resource: "storage_bytes", Current: e.Usage.ReservedBytes, Additional: out.AdditionalReservedBytes, Limit: e.Usage.StorageLimitBytes},
		{Resource: "pending_uploads", Current: e.Usage.PendingUploads, Additional: 1, Limit: e.Usage.PendingUploadsLimit},
		{Resource: "releases_per_site", Current: int64(len(e.Releases)), Additional: 1, Limit: int64(e.Capabilities.ReleasesPerSite)},
	}
	for _, q := range checks {
		if q.Current > math.MaxInt64-q.Additional {
			return errors.New("deployment planning usage overflows its accounting bound")
		}
		q.Projected = q.Current + q.Additional
		q.Blocked = q.Projected > q.Limit
		out.Quotas = append(out.Quotas, q)
		if q.Blocked {
			out.Blockers = append(out.Blockers, q.Resource)
		}
	}
	return nil
}
