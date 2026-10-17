package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func retainedStageCLI(t *testing.T, state string) *plannedStageCLI {
	t.Helper()
	f := newPlannedStageCLI(t)
	t.Setenv("FAAS_FRONTEND_HOSTING_STAGE_RECOVERY_ENABLED", "1")
	manifest, err := staticHostingManifest(f.dir, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload int64
	for _, file := range manifest.Files {
		payload += file.Size
	}
	f.release = api.HostingRelease{ID: reviewReleaseID, SiteID: reviewSiteID, State: state, Manifest: manifest, ArtifactBytes: payload}
	f.retained, f.usage.Releases = true, 1
	f.usage.ReservedBytes = api.HostingStorageCopies*payload + api.HostingManifestMaxBytes
	if state == "pending" {
		f.usage.PendingUploads = 1
	}
	return f
}

func recoveryArgs(f *plannedStageCLI) []string {
	return append(f.args(), "--resume-release", reviewReleaseID)
}

func TestSitesStageRecoveryReusesPendingReservationAtFullQuotas(t *testing.T) {
	// adr: 202610171431 — recovery never requests a new reservation or release.
	f := retainedStageCLI(t, "pending")
	f.usage.ReservedBytes = api.HostingAccountStorageMaxBytes + 1 // Legacy over-quota accounting also remains recoverable.
	f.usage.PendingUploads = api.HostingAccountPendingUploadsMax
	f.usage.Releases = api.HostingReleasesPerSite + api.HostingAccountPendingUploadsMax - 1
	for i := 1; i < api.HostingReleasesPerSite; i++ {
		f.otherReleases = append(f.otherReleases, api.HostingRelease{ID: fmt.Sprintf("44444444-4444-4444-8444-%012d", i), SiteID: reviewSiteID, State: "retiring"})
	}
	f.grantMutation = func() {
		if err := os.WriteFile(filepath.Join(f.dir, "index.html"), []byte("rebuild after recovery snapshot"), 0600); err != nil {
			t.Error(err)
		}
	}
	out := planCLIOutput(t)
	if code := cmdSites(recoveryArgs(f)); code != 0 {
		t.Fatalf("recovery exit=%d", code)
	}
	var result struct {
		State            string
		Resumed          bool
		Release          api.HostingRelease
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Resumed || result.State != "staged" || result.Release.ID != reviewReleaseID || result.ExpectedRevision != 7 || f.creates != 0 || f.finalizes != 1 || f.uploads["index.html"] != "hello" {
		t.Fatalf("result=%+v creates=%d finalizes=%d uploads=%v error=%v", result, f.creates, f.finalizes, f.uploads, err)
	}
	for _, request := range f.requests {
		if strings.Contains(request, "promotions") || request == "POST /v1/internal/hosting/sites/"+reviewSiteID+"/releases" {
			t.Fatal(request)
		}
	}
}

func TestSitesStageRecoveryReadyReleaseIsReadOnly(t *testing.T) {
	f := retainedStageCLI(t, "ready")
	f.usage.ReservedBytes = api.HostingAccountStorageMaxBytes
	f.usage.PendingUploads = api.HostingAccountPendingUploadsMax
	f.usage.Releases += api.HostingAccountPendingUploadsMax // Pending uploads belong to other sites.
	out := planCLIOutput(t)
	if code := cmdSites(recoveryArgs(f)); code != 0 {
		t.Fatal(code)
	}
	if f.creates != 0 || f.finalizes != 0 || len(f.uploads) != 0 || f.inventoryReads != 2 {
		t.Fatalf("unexpected recovery work: %+v", f.requests)
	}
	for _, request := range f.requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatal("ready recovery mutated: " + request)
		}
	}
	var result struct{ Resumed bool }
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Resumed {
		t.Fatalf("output=%s error=%v", out, err)
	}
}

func TestSitesStageRecoveryRecognizesAcceptedFinalizationOnExplicitRetry(t *testing.T) {
	f := retainedStageCLI(t, "pending")
	f.mode = "finalize_accepted_lost"
	out := planCLIOutput(t)
	if code := cmdSites(recoveryArgs(f)); code != 3 || f.finalizes != 1 || f.creates != 0 || out.Len() != 0 {
		t.Fatalf("exit=%d finalize=%d output=%s", code, f.finalizes, out)
	}
	f.mode, f.requests, f.inventoryReads = "", nil, 0
	if code := cmdSites(recoveryArgs(f)); code != 0 || f.finalizes != 1 || f.creates != 0 {
		t.Fatalf("exit=%d finalize=%d creates=%d", code, f.finalizes, f.creates)
	}
	for _, request := range f.requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatal("accepted finalization was repeated: " + request)
		}
	}
}

func TestSitesStageRecoveryRequiresFlagsPlanAndExactUUIDBeforeAPI(t *testing.T) {
	for _, mode := range []string{"main_off", "staging_off", "recovery_off", "empty_release", "bad_release", "missing_plan", "missing_no_promote", "wait", "changed_build", "wrong_origin"} {
		t.Run(mode, func(t *testing.T) {
			f := retainedStageCLI(t, "pending")
			args := recoveryArgs(f)
			switch mode {
			case "main_off":
				t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "")
			case "staging_off":
				t.Setenv("FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED", "")
			case "recovery_off":
				t.Setenv("FAAS_FRONTEND_HOSTING_STAGE_RECOVERY_ENABLED", "")
			case "empty_release":
				args[len(args)-1] = ""
			case "bad_release":
				args[len(args)-1] = "not-a-release"
			case "missing_plan":
				args = append(append([]string{}, args[:5]...), args[7:]...)
			case "missing_no_promote":
				args = append(append([]string{}, args[:7]...), args[8:]...)
			case "wait":
				args = append(args, "--wait")
			case "changed_build":
				if err := os.WriteFile(filepath.Join(f.dir, "index.html"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong_origin":
				t.Setenv("FAAS_API", "https://another.gregale.test")
			}
			out := planCLIOutput(t)
			if code := cmdSites(args); code == 0 || len(f.requests) != 0 || out.Len() != 0 {
				t.Fatalf("exit=%d requests=%v output=%s", code, f.requests, out)
			}
		})
	}
}

func TestSitesStageRecoveryRefusesIneligibleOrChangedReleaseBeforeWrites(t *testing.T) {
	for _, mode := range []string{"missing", "foreign", "retiring", "deleted", "previously_promoted", "active", "wrong_manifest", "wrong_accounting", "duplicate", "production_changed", "stage_capability", "scoped_credential", "ready_retired_during_read"} {
		t.Run(mode, func(t *testing.T) {
			f := retainedStageCLI(t, "ready")
			switch mode {
			case "missing":
				f.release.ID = "44444444-4444-4444-8444-444444444444"
			case "foreign":
				f.release.SiteID = "44444444-4444-4444-8444-444444444444"
			case "retiring", "deleted":
				f.release.State = mode
			case "previously_promoted":
				at := time.Now()
				f.release.FirstPromotedAt = &at
			case "active":
				f.site.ActiveReleaseID = f.release.ID
			case "wrong_manifest":
				f.release.Manifest.SPAFallback = true
			case "wrong_accounting":
				f.release.ArtifactBytes++
			case "duplicate":
				f.otherReleases = []api.HostingRelease{f.release}
				f.usage.Releases++
			case "production_changed":
				f.site.Revision++
			case "stage_capability":
				f.caps.StagedReleasesSupported = false
			case "scoped_credential":
				f.caps.AutomationSiteID = f.site.ID
			case "ready_retired_during_read":
				f.inventoryMutation = func(n int) {
					if n == 2 {
						f.release.State = "retiring"
					}
				}
			}
			out := planCLIOutput(t)
			if code := cmdSites(recoveryArgs(f)); code == 0 || f.creates != 0 || f.finalizes != 0 || len(f.uploads) != 0 || out.Len() != 0 {
				t.Fatalf("exit=%d creates=%d finalize=%d output=%s", code, f.creates, f.finalizes, out)
			}
			for _, request := range f.requests {
				if !strings.HasPrefix(request, "GET ") {
					t.Fatal(request)
				}
			}
		})
	}
}

func TestSitesStageRecoveryFailureNeverAllocatesOrAutomaticallyRetries(t *testing.T) {
	for _, mode := range []string{"wrong_grants", "credential_header", "upload_failure", "finalize_failure", "wrong_finalize", "after_finalize"} {
		t.Run(mode, func(t *testing.T) {
			f := retainedStageCLI(t, "pending")
			f.mode = mode
			out := planCLIOutput(t)
			if code := cmdSites(recoveryArgs(f)); code == 0 || f.creates != 0 || f.finalizes > 1 || out.Len() != 0 {
				t.Fatalf("exit=%d creates=%d finalize=%d output=%s", code, f.creates, f.finalizes, out)
			}
		})
	}
}

func TestSitesStageRecoveryPreservesImmutableSourceLabels(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			f := retainedStageCLI(t, "ready")
			f.caps.HistoryEnabled = true
			t.Setenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED", "1")
			f.release.Provenance = &api.HostingReleaseProvenance{Repository: "org/site"}
			label := "org/site"
			if changed {
				label = "org/changed"
			}
			code := cmdSites(append(recoveryArgs(f), "--source-repository", label))
			if (code == 0) == changed || f.creates != 0 || f.finalizes != 0 {
				t.Fatalf("exit=%d changed=%v", code, changed)
			}
		})
	}
}

func TestSitesStageRecoveryDoesNotIgnoreConfigOrUnknownBlockers(t *testing.T) {
	for _, blocker := range []string{"config_disabled", "future_admission_rule"} {
		if err := checkRetainedHostingPlan(api.HostingDeploymentPlan{}, api.HostingDeploymentPlan{State: "blocked", Blockers: []string{blocker}}); err == nil {
			t.Fatal("ignored blocker: " + blocker)
		}
	}
	if err := checkRetainedHostingPlan(api.HostingDeploymentPlan{}, api.HostingDeploymentPlan{State: "unavailable"}); err == nil {
		t.Fatal("accepted unknown recovery evidence state")
	}
}

func TestSitesStageRecoveryRespectsServerConfigurationFlag(t *testing.T) {
	f := retainedStageCLI(t, "pending")
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "1")
	f.caps.ConfigEnabled = true
	if err := os.WriteFile(filepath.Join(f.dir, "gregale-hosting.json"), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.regeneratePlan(t)
	manifest, err := staticHostingManifest(f.dir, false)
	if err != nil {
		t.Fatal(err)
	}
	f.release.Manifest = manifest
	f.caps.ConfigEnabled = false
	out := planCLIOutput(t)
	if code := cmdSites(recoveryArgs(f)); code == 0 || f.creates != 0 || f.finalizes != 0 || out.Len() != 0 {
		t.Fatalf("exit=%d output=%s", code, out)
	}
	for _, request := range f.requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatal("configuration-disabled recovery mutated: " + request)
		}
	}
}
