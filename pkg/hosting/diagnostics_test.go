package hosting

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func diagnosticFixture(now time.Time) DiagnosticEvidence {
	site := "11111111-1111-4111-8111-111111111111"
	run := "22222222-2222-4222-8222-222222222222"
	release := "33333333-3333-4333-8333-333333333333"
	preview := "44444444-4444-4444-8444-444444444444"
	return DiagnosticEvidence{Site: api.HostingSite{ID: site, OriginID: strings.Repeat("a", 64), Status: "active", DurableRouting: true, ActiveReleaseID: release}, RunID: run, Run: &api.HostingCIRun{ID: run, SiteID: site, ReleaseID: release, PreviewID: preview, CreatedAt: now.Add(-time.Minute), PreviewExpiresAt: now.Add(time.Hour)}, Release: &api.HostingRelease{ID: release, SiteID: site, State: "pending", Manifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", SHA256: strings.Repeat("b", 64), Size: 5, ContentType: "text/html"}}}}, Publication: api.HostingPublicationStatus{State: "published", Generation: 2, PublishedGeneration: 2, PublishedAt: &now}, AutomationAvailable: true, PreviewsAvailable: true}
}
func diagnosticCode(report api.HostingDiagnostics, step string) string {
	for _, c := range report.Checks {
		if c.Step == step {
			return c.Code
		}
	}
	return ""
}

func TestHostingDiagnosticsRecoveryClassification(t *testing.T) {
	// adr: 202610171059 — missing, expired and inconsistent intent never authorizes replacement allocation.
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	preview := func(e *DiagnosticEvidence) {
		e.Run.PreviewCreated = true
		e.Release.State = "ready"
		e.Preview = &api.HostingPreview{ID: e.Run.PreviewID, SiteID: e.Site.ID, ReleaseID: e.Run.ReleaseID, OriginID: e.Site.OriginID, ExpiresAt: e.Run.PreviewExpiresAt, Generation: 1, PublishedGeneration: 1, PublishedAt: &now}
	}
	for _, test := range []struct {
		name, step, code, advice string
		change                   func(*DiagnosticEvidence)
	}{
		{"uploads", "release", "release_pending", "retry_same_run", func(*DiagnosticEvidence) {}},
		{"verified awaiting preview", "preview", "preview_not_created", "retry_same_run", func(e *DiagnosticEvidence) { e.Release.State = "ready" }},
		{"acknowledged private preview", "preview", "publication_acknowledged", "none", preview},
		{"origin failure", "publication", "origin_unavailable", "retry_same_run", func(e *DiagnosticEvidence) {
			e.Publication.PublishedGeneration = 1
			e.Publication.LastErrorCode = api.HostingRouteErrorOrigin
		}},
		{"unknown provider payload", "publication", "publication_retry", "retry_same_run", func(e *DiagnosticEvidence) {
			e.Publication.PublishedGeneration = 1
			e.Publication.LastErrorCode = "private-provider-token"
		}},
		{"expired run", "run", "run_expired", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Run.PreviewExpiresAt = now }},
		{"removed release", "release", "release_removed", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Release = nil }},
		{"retiring release", "release", "release_retiring", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Release.State = "retiring" }},
		{"removed preview", "preview", "preview_removed", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Run.PreviewCreated = true }},
		{"revoked preview", "preview", "preview_revoked", "inspect_before_retry", func(e *DiagnosticEvidence) { preview(e); e.Preview.Revoked = true }},
		{"wrong preview expiry", "preview", "preview_inconsistent", "inspect_before_retry", func(e *DiagnosticEvidence) { preview(e); e.Preview.ExpiresAt = now.Add(2 * time.Hour) }},
		{"unknown run", "run", "run_not_found", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Run = nil }},
		{"automation off", "run", "automation_unavailable", "inspect_before_retry", func(e *DiagnosticEvidence) { e.AutomationAvailable = false }},
		{"previews off", "preview", "previews_unavailable", "inspect_before_retry", func(e *DiagnosticEvidence) { e.PreviewsAvailable = false }},
		{"suspended", "site", "site_suspended", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Site.Suspended = true }},
		{"deleting", "site", "site_deleting", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Site.Status = "deleting" }},
		{"different run site", "run", "run_inconsistent", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Run.SiteID = "other" }},
		{"different release site", "release", "release_inconsistent", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Release.SiteID = "other" }},
		{"repair failed after acknowledgment", "publication", "publication_repair_failed", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Publication.LastErrorCode = api.HostingRouteErrorProvider }},
		{"missing receipt", "publication", "publication_receipt_missing", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Publication.PublishedAt = nil }},
		{"generation ahead", "publication", "publication_inconsistent", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Publication.PublishedGeneration = 3 }},
		{"changed during read", "observation", "state_changed", "inspect_before_retry", func(e *DiagnosticEvidence) { e.Changed = true }},
		{"no production release", "release", "no_active_release", "inspect_before_retry", func(e *DiagnosticEvidence) { e.RunID = ""; e.Site.ActiveReleaseID = "" }},
		{"legacy site", "publication", "publication_untracked", "inspect_before_retry", func(e *DiagnosticEvidence) {
			e.RunID = ""
			e.Publication = api.HostingPublicationStatus{State: "untracked"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := diagnosticFixture(now)
			test.change(&e)
			before, _ := json.Marshal(e)
			out := DiagnoseHosting(e, now.Add(-time.Second), now)
			after, _ := json.Marshal(e)
			if out.RetryAdvice != test.advice || diagnosticCode(out, test.step) != test.code {
				t.Fatalf("%s: %+v", test.code, out)
			}
			if out.ServingHealth != "not_checked" || diagnosticCode(out, "serving") != "serving_not_checked" || string(before) != string(after) {
				t.Fatal("invented serving health or mutated evidence")
			}
		})
	}
}

func TestHostingDiagnosticReportDoesNotEchoPrivateFields(t *testing.T) {
	e := diagnosticFixture(time.Now().UTC())
	e.Site.Hostname = "private-host-secret"
	e.Site.OriginID = "private-origin-secret"
	e.Release.Provenance = &api.HostingReleaseProvenance{Description: "private-source-secret"}
	e.Release.Manifest.Files[0].Path = "private-file-secret"
	e.Publication.PublishedGeneration = 1
	e.Publication.LastErrorCode = "https://provider.example/?token=private-provider-secret"
	out := DiagnoseHosting(e, time.Now(), time.Now())
	raw, _ := json.Marshal(out)
	for _, secret := range []string{"private-host-secret", "private-origin-secret", "private-source-secret", "private-file-secret", "private-provider-secret", "provider.example"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private data escaped", secret)
		}
	}
	if len(out.ManifestFingerprint) != 64 {
		t.Fatal("missing bounded manifest identity")
	}
}
