package main

// Parked-state labels shared by the list and status views. A deployment
// parked after repeated liveness failures stayed "live" in `release list`
// and `project info`, and `status` printed the raw "evicted_cold", so only
// `inspect` said the app was down and why (2026-11-12 agent feedback).

import (
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const appStatusParked = "evicted_cold"

// parkedStatusLabel names a parked app with its reason when one is known.
func parkedStatusLabel(reason string) string {
	switch reason {
	case "liveness_exhausted":
		return "parked: liveness failed"
	case "security_scan_regressed":
		return "parked: security scan"
	case "admin_park":
		return "parked by Gregale"
	}
	return "parked"
}

// appParkedLabel is "" for an app that is not parked.
func appParkedLabel(app api.AppResponse) string {
	if app.Status != appStatusParked {
		return ""
	}
	return parkedStatusLabel(inspectParkedReason(app))
}

// parkedDeploymentID is the deployment the app is parked on, or "" when the
// app is not parked or the API did not say which deployment it was.
func parkedDeploymentID(app api.AppResponse) string {
	if app.Status != appStatusParked || app.ParkedDeployment == nil {
		return ""
	}
	return app.ParkedDeployment.ID
}

// parkedReleaseStatus is the release list status for a deployment that is
// still the app's live release while the app is parked.
// listedReleaseStatus is the STATUS column of a release row. A canary whose
// rollout was aborted keeps status "live" with 0% traffic while its
// predecessor serves again, and the list called it plain "live" (prod hunt
// 2026-11-20, R10-8).
func listedReleaseStatus(d api.DeploymentResponse) string {
	if d.Status == "live" && d.RolloutState == api.DebugRolloutAborted && d.TrafficPercent == 0 {
		return "aborted"
	}
	// A canary at 1% and the release it replaces at 99% both listed as plain
	// "live", so the list could not say which one served (boa multi-service
	// deploy, 2026-11-21). The share rides in the STATUS cell, as "parked"
	// does, so the default table keeps its column layout for scripts.
	if d.Status == "live" && d.TrafficPercent > 0 && d.TrafficPercent < 100 {
		return fmt.Sprintf("live/%d%%", d.TrafficPercent)
	}
	return d.Status
}

// servingReleaseApps returns the apps with a listed release taking traffic.
// Beside one, a live release at 0% is a flip-back standby: an MCP redeploy (or
// a traffic flip) keeps the previous release live at 0% so traffic can move
// back instantly, and it is superseded when its pins expire. The list called
// both "live" (MCP Tasks prod re-probe 2026-11-23). A sole live release with
// no recorded share stays "live".
func servingReleaseApps(items []api.DeploymentResponse) map[string]bool {
	serving := map[string]bool{}
	for _, d := range items {
		if d.Status == "live" && d.TrafficPercent > 0 {
			serving[d.AppID] = true
		}
	}
	return serving
}

func parkedReleaseStatus(status string) string {
	if status == "live" || strings.HasPrefix(status, "live/") {
		return status + "/parked"
	}
	return status
}

// writeParkedNotice explains a parked app in one line and gives the next
// command. Only a liveness park is a failure; a plain park is scale to zero.
func writeParkedNotice(w io.Writer, app api.AppResponse) {
	if app.Status != appStatusParked {
		return
	}
	switch inspectParkedReason(app) {
	case "liveness_exhausted":
		_, _ = fmt.Fprintf(w, "! %s is parked: its instances repeatedly failed liveness checks or crashed, so callers get errors or timeouts. Run `gregale app health %s --explain`.\n", app.Slug, app.Slug)
	case "security_scan_regressed":
		_, _ = fmt.Fprintf(w, "! %s is parked: its image failed a security re-scan. Run `gregale app health %s --explain`.\n", app.Slug, app.Slug)
	}
}
