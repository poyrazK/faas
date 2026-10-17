package hosting

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func deploymentPlanFixture() (DeploymentPlanEvidence, api.HostingManifest) {
	site := api.HostingSite{ID: "11111111-1111-4111-8111-111111111111", AccountID: "33333333-3333-4333-8333-333333333333", Status: "active", Revision: 7}
	return DeploymentPlanEvidence{
		Site: site,
		Capabilities: api.HostingCapabilities{FilesMax: api.HostingFilesMax, FileMaxBytes: api.HostingFileMaxBytes, ReleaseMaxBytes: api.HostingReleaseMaxBytes, ManifestMaxBytes: api.HostingManifestMaxBytes,
			ReleasesPerSite: api.HostingReleasesPerSite, AccountStorageMaxBytes: api.HostingAccountStorageMaxBytes, AccountPendingUploadsMax: api.HostingAccountPendingUploadsMax},
		Usage: api.HostingUsage{AccountID: site.AccountID, StorageLimitBytes: api.HostingAccountStorageMaxBytes, PendingUploadsLimit: api.HostingAccountPendingUploadsMax, ObservedAt: time.Now().UTC()},
	}, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 10, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}}
}

func planReleases(e *DeploymentPlanEvidence, n int) {
	for i := range n {
		e.Releases = append(e.Releases, api.HostingRelease{ID: fmt.Sprintf("22222222-2222-4222-8222-%012d", i), SiteID: e.Site.ID, State: "retiring"})
	}
	e.Usage.Releases = int64(n)
}

func TestDeploymentPlanComparisonMatchesVerifiedReleaseReview(t *testing.T) {
	// adr: 202610171338 — local plans and uploaded reviews share manifest semantics.
	e, local := deploymentPlanFixture()
	before := api.HostingManifest{Files: []api.HostingFile{local.Files[0], {Path: "old.js", Size: 4, SHA256: strings.Repeat("a", 64), ContentType: "text/javascript"}}}
	local.Files[0].ContentType = "text/plain"
	local.Files = append(local.Files, api.HostingFile{Path: "new.js", Size: 6, SHA256: strings.Repeat("b", 64), ContentType: "text/javascript"})
	local.SPAFallback = true
	local.Config = &api.HostingReleaseConfig{Version: 1, Redirects: []api.HostingRedirect{{From: "/old", To: "/new", Status: 308}}, Headers: []api.HostingHeaderRule{{Path: "/*", Values: map[string]string{"x-frame-options": "DENY"}}}}
	e.Capabilities.ConfigEnabled = true
	planReleases(&e, 1)
	e.Site.ActiveReleaseID = e.Releases[0].ID
	e.Releases[0].State, e.Releases[0].Manifest = "ready", before
	plan, err := PlanDeployment(e, local, time.Now().UTC(), time.Now().UTC())
	if err != nil || plan.State != "within_observed_limits" || !plan.Advisory || plan.FileCount != 2 || plan.PayloadBytes != 16 || plan.AdditionalReservedBytes != 2*16+api.HostingManifestMaxBytes || plan.ExpectedRevision != 7 || plan.BaseReleaseID != e.Site.ActiveReleaseID {
		t.Fatalf("plan %+v: %v", plan, err)
	}
	target := api.HostingRelease{ID: "target", SiteID: e.Site.ID, State: "ready", Manifest: local}
	review, err := ReviewRelease(e.Site, append(e.Releases, target), target.ID)
	if err != nil || !reflect.DeepEqual(plan.Changes, review.HostingManifestDiff) || plan.Changes.Added != 1 || plan.Changes.Changed != 1 || plan.Changes.Removed != 1 || !plan.Changes.SettingsChanged {
		t.Fatalf("plan/review drift: %+v %+v %v", plan.Changes, review, err)
	}
	encoded, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &wire); err != nil || string(wire["added"]) != "1" || len(wire["files"]) == 0 || wire["HostingManifestDiff"] != nil {
		t.Fatalf("existing review JSON changed: %s %v", encoded, err)
	}
}

func TestDeploymentPlanQuotaBoundaries(t *testing.T) {
	// adr: 202610171338 — prospective capacity includes a new, full reservation.
	reservation := 2*int64(10) + api.HostingManifestMaxBytes
	for _, tc := range []struct {
		name              string
		reserved, pending int64
		releases          int
		blocked           []string
	}{
		{name: "empty"},
		{name: "exact_storage", reserved: api.HostingAccountStorageMaxBytes - reservation},
		{name: "one_byte_short", reserved: api.HostingAccountStorageMaxBytes - reservation + 1, blocked: []string{"storage_bytes"}},
		{name: "already_over_quota", reserved: api.HostingAccountStorageMaxBytes + 1, blocked: []string{"storage_bytes"}},
		{name: "last_pending_slot", pending: 3},
		{name: "pending_full", pending: 4, blocked: []string{"pending_uploads"}},
		{name: "last_release_slot", releases: 9},
		{name: "retiring_rows_still_count", releases: 10, blocked: []string{"releases_per_site"}},
		{name: "all_full", reserved: api.HostingAccountStorageMaxBytes, pending: 4, releases: 10, blocked: []string{"storage_bytes", "pending_uploads", "releases_per_site"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, local := deploymentPlanFixture()
			planReleases(&e, tc.releases)
			e.Usage.Releases += tc.pending // Other sites share this account's pending slots.
			e.Usage.ReservedBytes, e.Usage.PendingUploads = tc.reserved, tc.pending
			out, err := PlanDeployment(e, local, time.Now(), time.Now())
			if err != nil || !reflect.DeepEqual(out.Blockers, append([]string{}, tc.blocked...)) || (out.State == "blocked") != (len(tc.blocked) > 0) || len(out.Quotas) != 3 || out.Quotas[0].Projected != tc.reserved+reservation {
				t.Fatalf("plan %+v: %v", out, err)
			}
		})
	}
}

func TestDeploymentPlanUnchangedAndEmptyFilesStillReserveStorage(t *testing.T) {
	e, local := deploymentPlanFixture()
	local.Files[0].Size = 0
	planReleases(&e, 1)
	e.Site.ActiveReleaseID = e.Releases[0].ID
	e.Releases[0].State, e.Releases[0].Manifest = "ready", local
	out, err := PlanDeployment(e, local, time.Now(), time.Now())
	if err != nil || len(out.Changes.Files) != 0 || out.AdditionalReservedBytes != api.HostingManifestMaxBytes || out.Quotas[1].Additional != 1 || out.Quotas[2].Additional != 1 || out.ManifestFingerprint != HostingManifestFingerprint(local) {
		t.Fatalf("plan %+v: %v", out, err)
	}
}

func TestDeploymentPlanConfigAndSuspension(t *testing.T) {
	e, local := deploymentPlanFixture()
	e.Site.Suspended = true
	local.Config = &api.HostingReleaseConfig{Version: 1}
	out, err := PlanDeployment(e, local, time.Now(), time.Now())
	if err != nil || out.State != "blocked" || !reflect.DeepEqual(out.Blockers, []string{"config_disabled"}) || len(out.Warnings) != 2 {
		t.Fatalf("plan %+v: %v", out, err)
	}
}

func TestDeploymentPlanFailsClosedOnInvalidEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*DeploymentPlanEvidence){
		"missing_baseline":  func(e *DeploymentPlanEvidence) { e.Site.ActiveReleaseID = "22222222-2222-4222-8222-222222222222" },
		"foreign_release":   func(e *DeploymentPlanEvidence) { e.Releases[0].SiteID = "foreign" },
		"duplicate_release": func(e *DeploymentPlanEvidence) { e.Releases = append(e.Releases, e.Releases[0]); e.Usage.Releases++ },
		"deleted_release":   func(e *DeploymentPlanEvidence) { e.Releases[0].State = "deleted" },
		"pending_baseline":  func(e *DeploymentPlanEvidence) { e.Site.ActiveReleaseID = e.Releases[0].ID },
		"invalid_baseline": func(e *DeploymentPlanEvidence) {
			e.Site.ActiveReleaseID = e.Releases[0].ID
			e.Releases[0].State = "ready"
		},
		"wrong_account":       func(e *DeploymentPlanEvidence) { e.Usage.AccountID = "other" },
		"missing_observation": func(e *DeploymentPlanEvidence) { e.Usage.ObservedAt = time.Time{} },
		"inactive_site":       func(e *DeploymentPlanEvidence) { e.Site.Status = "deleting" },
		"negative_storage":    func(e *DeploymentPlanEvidence) { e.Usage.ReservedBytes = -1 },
		"negative_pending":    func(e *DeploymentPlanEvidence) { e.Usage.PendingUploads = -1 },
		"overflow_storage":    func(e *DeploymentPlanEvidence) { e.Usage.ReservedBytes = math.MaxInt64 },
		"overflow_pending": func(e *DeploymentPlanEvidence) {
			e.Usage.PendingUploads = math.MaxInt64
			e.Usage.Releases = math.MaxInt64
		},
		"inconsistent_count":     func(e *DeploymentPlanEvidence) { e.Usage.Releases = 0 },
		"mismatched_limits":      func(e *DeploymentPlanEvidence) { e.Capabilities.FilesMax-- },
		"mismatched_usage_limit": func(e *DeploymentPlanEvidence) { e.Usage.StorageLimitBytes-- },
		"scoped_credential":      func(e *DeploymentPlanEvidence) { e.Capabilities.AutomationSiteID = e.Site.ID },
	} {
		t.Run(name, func(t *testing.T) {
			e, local := deploymentPlanFixture()
			planReleases(&e, 1)
			mutate(&e)
			if _, err := PlanDeployment(e, local, time.Now(), time.Now()); err == nil {
				t.Fatal("invalid evidence produced a plan")
			}
		})
	}
}

func TestDeploymentPlanRejectsInvalidAndOversizedManifests(t *testing.T) {
	e, local := deploymentPlanFixture()
	local.Files[0].SHA256 = "bad"
	if _, err := PlanDeployment(e, local, time.Now(), time.Now()); err == nil {
		t.Fatal("invalid manifest planned")
	}
	e, local = deploymentPlanFixture()
	for i := 1; i < api.HostingFilesMax; i++ {
		f := local.Files[0]
		f.Path = fmt.Sprintf("%04d-%s", i, strings.Repeat("a", 490))
		local.Files = append(local.Files, f)
	}
	if err := ValidateManifest(local); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanDeployment(e, local, time.Now(), time.Now()); err == nil {
		t.Fatal("oversized serialized manifest planned")
	}
}
