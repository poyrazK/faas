package hosting

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostingReleaseReview(t *testing.T) {
	// adr: 202610161920 — comparisons bind immutable content to reviewed intent.
	file := func(path string, size int64) api.HostingFile {
		return api.HostingFile{Path: path, Size: size, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}
	}
	site := api.HostingSite{ID: "site", Status: "active", Revision: 9, ActiveReleaseID: "old"}
	previous := api.HostingRelease{ID: "old", SiteID: "site", State: "ready", Manifest: api.HostingManifest{Files: []api.HostingFile{file("index.html", 3), file("removed.html", 4), file("mime.html", 5)}}}
	next := api.HostingRelease{ID: "new", SiteID: "site", State: "ready", Manifest: api.HostingManifest{SPAFallback: true, Config: &api.HostingReleaseConfig{Version: 1, Redirects: []api.HostingRedirect{{From: "/old", To: "/new", Status: 308}}}, Files: []api.HostingFile{file("new.html", 7), file("index.html", 8), file("mime.html", 5)}}}
	next.Manifest.Files[2].ContentType = "text/plain"
	out, err := ReviewRelease(site, []api.HostingRelease{previous, next}, "new")
	if err != nil || out.Added != 1 || out.Changed != 2 || out.Removed != 1 || out.BeforeBytes != 12 || out.AfterBytes != 20 || out.ExpectedRevision != 9 || !out.SettingsChanged {
		t.Fatalf("review %+v %v", out, err)
	}
	if out.Files[0].Path != "index.html" || out.Files[3].Path != "removed.html" || out.AfterSettings.Config.Redirects[0].To != "/new" {
		t.Fatalf("unstable or missing comparison: %+v", out)
	}
	// A missing active baseline must never masquerade as a first deployment.
	if _, err = ReviewRelease(site, []api.HostingRelease{next}, "new"); err == nil {
		t.Fatal("invented empty baseline")
	}
	for _, state := range []string{"pending", "retiring"} {
		invalid := next
		invalid.State = state
		if _, err = ReviewRelease(site, []api.HostingRelease{previous, invalid}, "new"); err == nil {
			t.Fatal("reviewed unverified target")
		}
	}
	foreign := next
	foreign.SiteID = "other"
	if _, err = ReviewRelease(site, []api.HostingRelease{previous, foreign}, "new"); err == nil {
		t.Fatal("cross-site release")
	}
	site.ActiveReleaseID = ""
	first, err := ReviewRelease(site, []api.HostingRelease{next}, "new")
	if err != nil || first.Added != 3 || first.BeforeBytes != 0 {
		t.Fatalf("first review %+v %v", first, err)
	}
}
