package activity

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

// Summarize reuses diagnostics' closed classifications. Production publication is
// intentionally excluded: an unrelated production release says nothing about CI.
func Summarize(site api.HostingSite, run api.HostingCIRun, release *api.HostingRelease, preview *api.HostingPreview, previews, source bool, now time.Time) api.HostingActivityEntry {
	out := api.HostingActivityEntry{RunID: run.ID, ReleaseID: run.ReleaseID, PreviewID: run.PreviewID, CreatedAt: run.CreatedAt, PreviewExpiresAt: run.PreviewExpiresAt, Canceled: run.Canceled, State: "recorded", Checks: []api.HostingDiagnosticCheck{}}
	report := hosting.DiagnoseHosting(hosting.DiagnosticEvidence{Site: site, RunID: run.ID, Run: &run, Release: release, Preview: preview, AutomationAvailable: true, PreviewsAvailable: previews}, now, now)
	priority := map[string]int{"recorded": 0, "pending": 1, "unknown": 2, "blocked": 3}
	for _, check := range report.Checks {
		if check.Step != "site" && check.Step != "run" && check.Step != "release" && check.Step != "preview" {
			continue
		}
		out.Checks = append(out.Checks, check)
		if priority[check.State] > priority[out.State] {
			out.State = check.State
		}
	}
	if source && release != nil && release.ID == run.ReleaseID && release.SiteID == site.ID && release.Provenance != nil {
		copy := *release.Provenance
		out.Provenance = &copy
	}
	if run.Canceled {
		out.State = "canceled"
	}
	return out
}
