package main

// Parked-state labels shared by the list and status views. A deployment
// parked after repeated liveness failures stayed "live" in `release list`
// and `project info`, and `status` printed the raw "evicted_cold", so only
// `inspect` said the app was down and why (2026-11-12 agent feedback).

import (
	"fmt"
	"io"

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
func parkedReleaseStatus(status string) string {
	if status == "live" {
		return "live/parked"
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
		_, _ = fmt.Fprintf(w, "! %s is parked: its instances repeatedly failed liveness checks or crashed, so callers get errors or timeouts. Run `gregale inspect %s`.\n", app.Slug, app.Slug)
	case "security_scan_regressed":
		_, _ = fmt.Fprintf(w, "! %s is parked: its image failed a security re-scan. Run `gregale inspect %s`.\n", app.Slug, app.Slug)
	}
}
