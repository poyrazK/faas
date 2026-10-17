package activity

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCursorIsCanonicalAndBounded(t *testing.T) {
	at := time.Date(2026, 10, 17, 11, 47, 0, 123456000, time.UTC)
	id := "abcdefab-1111-4111-8111-111111111111"
	raw := Cursor(at, id)
	got, gotID, err := ParseCursor(raw)
	if err != nil || !got.Equal(at) || gotID != id {
		t.Fatal(got, gotID, err)
	}
	if _, _, err = ParseCursor(""); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"bad", raw + "=", strings.Repeat("a", api.HostingActivityCursorMaxBytes+1), base64.RawURLEncoding.EncodeToString([]byte(at.Format(time.RFC3339Nano) + "|" + strings.ToUpper(id))), Cursor(at, "00000000-0000-0000-0000-000000000000"), base64.RawURLEncoding.EncodeToString([]byte("2026-10-17T11:47:00.123456+00:00|" + id))} {
		if _, _, err = ParseCursor(bad); err == nil {
			t.Fatal("accepted cursor", bad)
		}
	}
}
func TestActivitySummariesDescribeStoredEvidenceOnly(t *testing.T) {
	// adr: 202610171147 — a recorded intent is not a GitHub job or serving-health assertion.
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	site := api.HostingSite{ID: "site", Status: "active", OriginID: "internal-origin", ActiveReleaseID: "unrelated-production"}
	run := api.HostingCIRun{ID: "run", SiteID: "site", ReleaseID: "release", PreviewID: "preview", CreatedAt: now.Add(-time.Hour), PreviewExpiresAt: now.Add(time.Hour), PreviewCreated: true, Fingerprint: "private-fingerprint"}
	r := api.HostingRelease{ID: "release", SiteID: "site", State: "ready", Provenance: &api.HostingReleaseProvenance{Description: "caller source"}}
	p := api.HostingPreview{ID: "preview", SiteID: "site", ReleaseID: "release", OriginID: site.OriginID, ExpiresAt: run.PreviewExpiresAt, Generation: 1, PublishedGeneration: 1, PublishedAt: &now, Hostname: "private-hostname"}
	for _, tc := range []struct {
		name, state, code string
		edit              func(*api.HostingCIRun, **api.HostingRelease, **api.HostingPreview)
		previews          bool
	}{
		{"recorded", "recorded", "publication_acknowledged", func(*api.HostingCIRun, **api.HostingRelease, **api.HostingPreview) {}, true},
		{"verification pending", "pending", "release_pending", func(_ *api.HostingCIRun, r **api.HostingRelease, _ **api.HostingPreview) { (*r).State = "pending" }, true},
		{"publication pending", "pending", "publication_pending", func(_ *api.HostingCIRun, _ **api.HostingRelease, p **api.HostingPreview) {
			(*p).PublishedGeneration = 0
			(*p).PublishedAt = nil
		}, true},
		{"removed release", "blocked", "release_removed", func(_ *api.HostingCIRun, r **api.HostingRelease, _ **api.HostingPreview) { *r = nil }, true},
		{"removed preview", "blocked", "preview_removed", func(_ *api.HostingCIRun, _ **api.HostingRelease, p **api.HostingPreview) { *p = nil }, true},
		{"expired", "blocked", "run_expired", func(r *api.HostingCIRun, _ **api.HostingRelease, _ **api.HostingPreview) {
			r.PreviewExpiresAt = now.Add(-time.Second)
		}, true},
		{"revoked", "blocked", "preview_revoked", func(_ *api.HostingCIRun, _ **api.HostingRelease, p **api.HostingPreview) { (*p).Revoked = true }, true},
		{"preview flag off", "unknown", "previews_unavailable", func(*api.HostingCIRun, **api.HostingRelease, **api.HostingPreview) {}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr, release, preview := run, r, p
			rp, pp := &release, &preview
			tc.edit(&rr, &rp, &pp)
			out := Summarize(site, rr, rp, pp, tc.previews, true, now)
			if out.State != tc.state {
				t.Fatal(out)
			}
			found := false
			for _, check := range out.Checks {
				if check.Code == tc.code {
					found = true
				}
				if check.Step == "publication" || check.Step == "serving" {
					t.Fatal("unrelated evidence", check)
				}
			}
			if !found {
				t.Fatal("missing explanation", out)
			}
			raw, _ := json.Marshal(out)
			for _, private := range []string{run.Fingerprint, p.Hostname, p.OriginID, site.ActiveReleaseID} {
				if strings.Contains(string(raw), private) {
					t.Fatal("private/unrelated data exposed", private)
				}
			}
		})
	}
	out := Summarize(site, run, &r, &p, true, false, now)
	if out.Provenance != nil {
		t.Fatal("history flag bypass")
	}
	out = Summarize(site, run, &r, &p, true, true, now)
	out.Provenance.Description = "changed"
	if r.Provenance.Description != "caller source" {
		t.Fatal("source alias")
	}
	p.LastErrorCode = "provider_secret_payload"
	out = Summarize(site, run, &r, &p, true, true, now)
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), p.LastErrorCode) || out.State != "unknown" {
		t.Fatal("raw provider error", out)
	}
}

func TestCanceledIntentRemainsTerminalInActivity(t *testing.T) {
	// adr: 202610171242 — canceled identities remain distinguishable after release and preview GC.
	site := api.HostingSite{ID: "site", Status: "active"}
	run := api.HostingCIRun{ID: "run", SiteID: site.ID, ReleaseID: "release", PreviewID: "preview", Canceled: true, CreatedAt: time.Now(), PreviewExpiresAt: time.Now().Add(time.Hour)}
	got := Summarize(site, run, nil, nil, true, false, time.Now())
	if got.State != "canceled" || !got.Canceled {
		t.Fatal(got)
	}
	found := false
	for _, c := range got.Checks {
		if c.Code == "run_canceled" && c.State == "blocked" {
			found = true
		}
	}
	if !found {
		t.Fatal("cancellation diagnostic missing", got)
	}
}
