package hosting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DiagnosticEvidence is assembled from existing read operations. Nil records
// mean a successful not-found read, never a failed read or a disabled feature.
// The collector must fail the request on storage errors.
type DiagnosticEvidence struct {
	Site                api.HostingSite
	Publication         api.HostingPublicationStatus
	RunID               string
	Run                 *api.HostingCIRun
	Release             *api.HostingRelease
	Preview             *api.HostingPreview
	AutomationAvailable bool
	PreviewsAvailable   bool
	Changed             bool
}

func HostingManifestFingerprint(manifest api.HostingManifest) string {
	data, _ := json.Marshal(manifest)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func DiagnoseHosting(e DiagnosticEvidence, started, now time.Time) api.HostingDiagnostics {
	out := api.HostingDiagnostics{SiteID: e.Site.ID, RunID: e.RunID, StartedAt: started, ObservedAt: now, ServingHealth: "not_checked", RetryAdvice: "none", Checks: []api.HostingDiagnosticCheck{}}
	add := func(step, state, code, summary, action string) {
		out.Checks = append(out.Checks, api.HostingDiagnosticCheck{Step: step, State: state, Code: code, Summary: summary, Action: action})
	}
	switch {
	case e.Site.Status == "deleting":
		add("site", "blocked", "site_deleting", "Site deletion has been requested.", "Inspect deletion progress; do not retry deployment to this site.")
	case e.Site.Suspended:
		add("site", "blocked", "site_suspended", "Site traffic is suspended.", "An operator must review and resume the site before deployment.")
	case e.Site.Status != "active":
		add("site", "unknown", "site_unknown", "Site state is not recognized.", "Refresh site state and ask an operator to inspect it.")
	default:
		add("site", "ok", "site_active", "Site intent is active.", "This does not establish that traffic is being served.")
	}
	if e.RunID != "" {
		diagnoseRun(&out, e, now)
	} else if e.Site.ActiveReleaseID == "" {
		add("release", "blocked", "no_active_release", "No release is selected for the production URL.", "Stage a release, review it, then explicitly promote it. CI previews do not promote production.")
	} else {
		out.ReleaseID = e.Site.ActiveReleaseID
		diagnoseRelease(&out, e)
	}
	out.Checks = append(out.Checks, diagnosePublication("publication", e.Publication))
	if e.Changed {
		add("observation", "unknown", "state_changed", "Site or CI intent changed while records were being read.", "Refresh diagnostics before deciding how to recover.")
	}
	add("serving", "not_checked", "serving_not_checked", "Serving health, DNS, TLS and Access sign-in were not tested.", "After publication, allow edge refresh and check the URL through the normal Access flow.")
	for _, check := range out.Checks {
		if check.State == "blocked" || check.State == "unknown" {
			out.RetryAdvice = "inspect_before_retry"
			return out
		}
		if check.State == "pending" && e.Run != nil {
			out.RetryAdvice = "retry_same_run"
		}
	}
	return out
}

func diagnoseRun(out *api.HostingDiagnostics, e DiagnosticEvidence, now time.Time) {
	check := api.HostingDiagnosticCheck{Step: "run", State: "ok", Code: "run_retained", Summary: "The original CI intent is retained.", Action: "Keep this run ID, its unchanged files, source labels and original preview TTL."}
	switch {
	case !e.AutomationAvailable:
		check.State, check.Code, check.Summary, check.Action = "unknown", "automation_unavailable", "CI records were not inspected because automation is disabled or unconfigured.", "Ask an operator to review automation configuration; do not replace the run ID."
	case e.Run == nil:
		check.State, check.Code, check.Summary, check.Action = "unknown", "run_not_found", "No retained intent was found for this site and run ID.", "Confirm the exact site and run IDs from CI output before retrying or creating an intent."
	case e.Run.ID != e.RunID || e.Run.SiteID != e.Site.ID || !e.Run.PreviewExpiresAt.After(e.Run.CreatedAt):
		check.State, check.Code, check.Summary, check.Action = "unknown", "run_inconsistent", "The retained intent does not match this diagnostic request.", "Refresh and ask an operator to inspect the retained intent."
	default:
		out.ReleaseID, out.PreviewID = e.Run.ReleaseID, e.Run.PreviewID
		if e.Run.Canceled {
			check.State, check.Code, check.Summary, check.Action = "blocked", "run_canceled", "This CI run was permanently canceled.", "Do not retry this intent. Preview revocation and safe artifact cleanup may still be pending."
		} else if !e.Run.PreviewExpiresAt.After(now) {
			check.State, check.Code, check.Summary, check.Action = "blocked", "run_expired", "The original CI preview window has expired.", "This run cannot extend or recreate its preview. Review retained resources before explicitly starting a new intent."
		}
	}
	out.Checks = append(out.Checks, check)
	if out.ReleaseID == "" {
		return
	}
	diagnoseRelease(out, e)
	diagnosePreview(out, e, now)
}

func diagnoseRelease(out *api.HostingDiagnostics, e DiagnosticEvidence) {
	check := api.HostingDiagnosticCheck{Step: "release", State: "blocked", Code: "release_removed", Summary: "The selected release record is absent.", Action: "Do not allocate a replacement by changing the run ID. Refresh and inspect retained intent and cleanup."}
	if e.Release != nil {
		r := e.Release
		switch {
		case r.ID != out.ReleaseID || r.SiteID != e.Site.ID:
			check.State, check.Code, check.Summary, check.Action = "unknown", "release_inconsistent", "Release identity does not match the selected intent.", "Refresh and ask an operator to inspect the selected release."
		case r.State == "pending":
			check.State, check.Code, check.Summary, check.Action = "pending", "release_pending", "Upload verification has not been acknowledged.", "If the run is still usable, resume it with unchanged files and metadata; no per-file upload completion is recorded."
		case r.State == "ready":
			check.State, check.Code, check.Summary, check.Action = "ok", "release_ready", "The release is verified.", "A verified release can remain staged; production promotion is a separate operator action."
		case r.State == "retiring" || r.State == "deleted":
			check.Code, check.Summary, check.Action = "release_retiring", "The release is retiring or removed.", "This retained CI run cannot allocate a replacement. Review cleanup before creating a new intent."
		default:
			check.State, check.Code, check.Summary, check.Action = "unknown", "release_unknown", "Release state is not recognized.", "Refresh before attempting recovery."
		}
		if (r.State == "pending" || r.State == "ready") && r.ID == out.ReleaseID && r.SiteID == e.Site.ID {
			out.ManifestFingerprint = HostingManifestFingerprint(r.Manifest)
		}
	}
	out.Checks = append(out.Checks, check)
}

func diagnosePreview(out *api.HostingDiagnostics, e DiagnosticEvidence, now time.Time) {
	check := api.HostingDiagnosticCheck{Step: "preview", State: "unknown", Code: "previews_unavailable", Summary: "Preview records were not inspected because previews are disabled or unconfigured.", Action: "Ask an operator to review preview configuration; do not assume the preview was removed."}
	if e.PreviewsAvailable {
		switch {
		case e.Preview == nil && !e.Run.PreviewCreated:
			check.State, check.Code, check.Summary, check.Action = "pending", "preview_not_created", "Preview creation has not been recorded.", "If the original run and release remain usable, resume the same run."
		case e.Preview == nil:
			check.State, check.Code, check.Summary, check.Action = "blocked", "preview_removed", "A previously created preview is absent.", "The retained run cannot recreate a removed preview. Inspect cleanup before choosing a new intent."
		case e.Preview.ID != out.PreviewID || e.Preview.SiteID != e.Site.ID || e.Preview.ReleaseID != out.ReleaseID || e.Preview.OriginID != e.Site.OriginID || !e.Preview.ExpiresAt.Equal(e.Run.PreviewExpiresAt):
			check.State, check.Code, check.Summary, check.Action = "unknown", "preview_inconsistent", "Preview identity or expiry does not match the retained intent.", "Refresh before attempting recovery."
		case e.Preview.Revoked:
			check.State, check.Code, check.Summary, check.Action = "blocked", "preview_revoked", "The preview has been revoked.", "Retrying this run will not replace the revoked preview. Review the retained resources."
		case !e.Preview.ExpiresAt.After(now):
			check.State, check.Code, check.Summary, check.Action = "blocked", "preview_expired", "The preview has expired.", "Retrying this run cannot extend the preview. Review cleanup before starting a new intent."
		default:
			p := e.Preview
			check = diagnosePublication("preview", api.HostingPublicationStatus{State: "pending", Generation: p.Generation, PublishedGeneration: p.PublishedGeneration, PublishedAt: p.PublishedAt, LastErrorCode: p.LastErrorCode, NextAttemptAt: &p.NextAttemptAt})
		}
	}
	out.Checks = append(out.Checks, check)
}

func diagnosePublication(step string, p api.HostingPublicationStatus) api.HostingDiagnosticCheck {
	check := api.HostingDiagnosticCheck{Step: step, State: "pending", Code: "publication_pending", Summary: "The current routing generation has not been acknowledged.", Action: "Wait for automatic publication retries, then refresh. A CI retry must keep the same run ID.", Generation: p.Generation, PublishedGeneration: p.PublishedGeneration}
	if p.NextAttemptAt != nil && !p.NextAttemptAt.IsZero() {
		at := *p.NextAttemptAt
		check.NextAttemptAt = &at
	}
	switch {
	case p.State == "untracked":
		check.State, check.Code, check.Summary, check.Action = "unknown", "publication_untracked", "This legacy site has no durable publication receipt.", "Ask an operator to inspect its routing mode; do not infer publication success."
	case p.Generation < 1 || p.PublishedGeneration < 0 || p.PublishedGeneration > p.Generation:
		check.State, check.Code, check.Summary, check.Action = "unknown", "publication_inconsistent", "Publication evidence is inconsistent.", "Refresh before deciding how to recover."
	case p.Generation == p.PublishedGeneration && p.PublishedAt != nil && !p.PublishedAt.IsZero():
		check.State, check.Code, check.Summary, check.Action = "ok", "publication_acknowledged", "The current routing generation has a provider acknowledgment.", "Allow edge refresh; this acknowledgment does not establish serving health."
	case p.Generation == p.PublishedGeneration:
		check.State, check.Code, check.Summary, check.Action = "unknown", "publication_receipt_missing", "The matching generation has no publication timestamp.", "Refresh and ask an operator to inspect the publication record."
	}
	if check.State == "ok" && p.LastErrorCode != "" {
		check.State, check.Code, check.Summary, check.Action = "unknown", "publication_repair_failed", "A provider acknowledgment exists, but the last routing repair attempt failed.", "Inspect publication repair and refresh after the next attempt; the old receipt does not establish current serving health."
		return check
	}
	if p.LastErrorCode != "" {
		// Provider payloads are untrusted. Only closed error classes become text.
		if check.State != "ok" {
			switch p.LastErrorCode {
			case api.HostingRouteErrorOrigin:
				check.Code, check.Summary = "origin_unavailable", "The publication origin is unavailable."
			case api.HostingRouteErrorProvider:
				check.Code, check.Summary = "provider_unavailable", "The edge publication service is unavailable."
			case api.HostingRouteErrorStale:
				check.Code, check.Summary = "publication_stale", "A newer routing generation is awaiting publication."
			default:
				check.Code, check.Summary = "publication_retry", "Publication needs attention; its last error was not recognized."
			}
		}
	}
	return check
}
