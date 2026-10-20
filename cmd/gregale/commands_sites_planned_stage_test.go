package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

type plannedStageCLI struct {
	dir, planFile, mode string
	site                api.HostingSite
	caps                api.HostingCapabilities
	usage               api.HostingUsage
	release             api.HostingRelease
	requests            []string
	uploads             map[string]string
	creates, finalizes  int
	mutation            func()
	retained            bool
	otherReleases       []api.HostingRelease
	inventoryReads      int
	inventoryMutation   func(int)
	grantMutation       func()
}

func newPlannedStageCLI(t *testing.T) *plannedStageCLI {
	t.Helper()
	dir, site, caps, usage := planCLIFixture(t)
	caps.StagedReleasesSupported = true
	f := &plannedStageCLI{dir: dir, site: site, caps: caps, usage: usage, uploads: map[string]string{}}
	t.Setenv("FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED", "1")
	t.Setenv("FAAS_FRONTEND_HOSTING_LOCAL_PREVIEW_ENABLED", "")
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unsafe storage request: %s %s", r.Method, r.URL.Path)
		}
		if f.mode == "upload_failure" {
			w.WriteHeader(503)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/hosting/staging/"+reviewSiteID+"/"+reviewReleaseID+"/")
		body, err := io.ReadAll(io.LimitReader(r.Body, api.HostingFileMaxBytes+1))
		if err != nil {
			t.Error(err)
		}
		f.uploads[name] = string(body)
		for _, file := range f.release.Manifest.Files {
			if file.Path == name && (r.ContentLength != file.Size || r.Header.Get("Content-Type") != file.ContentType) {
				t.Error("reviewed size/MIME changed")
			}
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(storage.Close)
	f.caps.UploadBaseURL = storage.URL
	previousTransport := http.DefaultTransport
	http.DefaultTransport = storage.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		base := "/v1/internal/hosting/sites/" + reviewSiteID
		var body any
		switch {
		case r.Method == "GET":
			if r.URL.Path == base+"/releases" && f.retained {
				f.inventoryReads++
				if f.inventoryMutation != nil {
					f.inventoryMutation(f.inventoryReads)
				}
				body = append([]api.HostingRelease{f.release}, f.otherReleases...)
				break
			}
			planCLIResponse(t, w, r, f.site, f.caps, f.usage)
			return
		case r.Method == "POST" && r.URL.Path == base+"/releases":
			f.creates++
			if f.mode == "create_failure" {
				w.WriteHeader(503)
				return
			}
			var req api.CreateHostingReleaseRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var payload int64
			for _, file := range req.Files {
				payload += file.Size
			}
			f.release = api.HostingRelease{ID: reviewReleaseID, SiteID: reviewSiteID, State: "pending", Manifest: req.HostingManifest, ArtifactBytes: payload, Provenance: req.Provenance}
			if f.mode == "wrong_creation" {
				f.release.Manifest.SPAFallback = !f.release.Manifest.SPAFallback
			}
			if f.mode == "after_create" {
				f.site.Revision++
			}
			if f.mutation != nil {
				f.mutation()
			}
			body = f.release
		case r.Method == "POST" && r.URL.Path == base+"/releases/"+reviewReleaseID+"/uploads":
			if f.grantMutation != nil {
				f.grantMutation()
			}
			grants := []api.HostingUpload{}
			for _, file := range f.release.Manifest.Files {
				grants = append(grants, api.HostingUpload{Path: file.Path, URL: storage.URL + "/hosting/staging/" + reviewSiteID + "/" + reviewReleaseID + "/" + file.Path + "?signature=must-not-print"})
			}
			if f.mode == "wrong_grants" {
				grants[0].URL = storage.URL + "/another-release?signature=must-not-print"
			}
			if f.mode == "credential_header" {
				grants[0].Headers = http.Header{"Authorization": []string{"must-not-forward"}}
			}
			body = api.HostingUploadResponse{Release: f.release, Uploads: grants}
		case r.Method == "POST" && r.URL.Path == base+"/releases/"+reviewReleaseID+"/finalize":
			f.finalizes++
			if f.mode == "finalize_failure" {
				w.WriteHeader(503)
				return
			}
			f.release.State = "ready"
			if f.mode == "finalize_accepted_lost" {
				w.WriteHeader(503)
				return
			}
			if f.mode == "wrong_finalize" {
				f.release.ID = "44444444-4444-4444-8444-444444444444"
			}
			if f.mode == "after_finalize" {
				f.site.Revision++
			}
			body = f.release
		default:
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(405)
			return
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	})
	f.planFile = filepath.Join(t.TempDir(), "plan.json")
	f.regeneratePlan(t)
	return f
}

func (f *plannedStageCLI) regeneratePlan(t *testing.T) {
	t.Helper()
	out := planCLIOutput(t)
	if code := main.CmdSites([]string{"plan", "--site", reviewSiteID, "--path", f.dir}); code != 0 {
		t.Fatalf("plan generation failed: %d", code)
	}
	if err := os.WriteFile(f.planFile, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	f.requests = nil
}

func (f *plannedStageCLI) args() []string {
	return []string{"deploy", "--site", reviewSiteID, "--path", f.dir, "--plan", f.planFile, "--no-promote"}
}

func TestSitesPlannedStageUsesFrozenReviewedBytesAndNeverPromotes(t *testing.T) {
	// adr: 202610171405 — mutations of the source after review cannot change uploads.
	f := newPlannedStageCLI(t)
	f.usage.ReservedBytes = 100 // Fresh usage may change while remaining within limits.
	f.mutation = func() {
		if err := os.WriteFile(filepath.Join(f.dir, "index.html"), []byte("changed after creation"), 0600); err != nil {
			t.Error(err)
		}
	}
	out := planCLIOutput(t)
	if code := main.CmdSites(f.args()); code != 0 {
		t.Fatalf("stage exit %d", code)
	}
	var result struct {
		State            string
		Release          api.HostingRelease
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.State != "staged" || result.Release.ID != reviewReleaseID || result.ExpectedRevision != 7 || result.Release.State != "ready" || f.creates != 1 || f.finalizes != 1 || f.uploads["index.html"] != "hello" {
		t.Fatalf("result %+v creates=%d finalizes=%d uploads=%v: %v", result, f.creates, f.finalizes, f.uploads, err)
	}
	for _, request := range f.requests {
		if strings.Contains(request, "promotions") {
			t.Fatal("planned staging promoted")
		}
	}
}

func TestSitesPlannedStageFlagsTargetsAndChangedBuildsBeforeAPI(t *testing.T) {
	for _, mode := range []string{"main_off", "flag_off", "no_no_promote", "empty_plan", "wrong_site", "wrong_origin", "changed_file", "changed_spa", "legacy_plan", "symlink_plan", "symlink_build"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlannedStageCLI(t)
			args := f.args()
			switch mode {
			case "main_off":
				t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "")
			case "flag_off":
				t.Setenv("FAAS_FRONTEND_HOSTING_PLANNED_STAGING_ENABLED", "")
			case "no_no_promote":
				args = args[:len(args)-1]
			case "empty_plan":
				args[len(args)-2] = ""
			case "wrong_site":
				args[2] = "44444444-4444-4444-8444-444444444444"
			case "wrong_origin":
				t.Setenv("FAAS_API", "https://another.gregale.test")
			case "changed_file":
				if err := os.WriteFile(filepath.Join(f.dir, "index.html"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed_spa":
				args = append(args, "--spa")
			case "legacy_plan":
				raw, _ := os.ReadFile(f.planFile)
				if err := os.WriteFile(f.planFile, bytes.Replace(raw, []byte(`"version": 1`), []byte(`"version": 0`), 1), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink_plan":
				link := filepath.Join(t.TempDir(), "linked.json")
				if err := os.Symlink(f.planFile, link); err != nil {
					t.Fatal(err)
				}
				args[len(args)-2] = link
			case "symlink_build":
				if err := os.Symlink("index.html", filepath.Join(f.dir, "link.html")); err != nil {
					t.Fatal(err)
				}
			}
			out := planCLIOutput(t)
			if code := main.CmdSites(args); code == 0 || len(f.requests) != 0 || out.Len() != 0 {
				t.Fatalf("exit=%d requests=%v output=%s", code, f.requests, out)
			}
		})
	}
}

func TestSitesPlannedStageFreshEvidenceBlocksBeforeCreation(t *testing.T) {
	for _, mode := range []string{"revision", "quota", "capability", "scoped_credential", "tampered_comparison"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlannedStageCLI(t)
			switch mode {
			case "revision":
				f.site.Revision++
			case "quota":
				f.usage.ReservedBytes = api.HostingAccountStorageMaxBytes
			case "capability":
				f.caps.StagedReleasesSupported = false
			case "scoped_credential":
				f.caps.AutomationSiteID = f.site.ID
			case "tampered_comparison":
				raw, _ := os.ReadFile(f.planFile)
				if err := os.WriteFile(f.planFile, bytes.Replace(raw, []byte(`"added": 1`), []byte(`"added": 0`), 1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out := planCLIOutput(t)
			if code := main.CmdSites(f.args()); code == 0 || f.creates != 0 || len(f.uploads) != 0 || out.Len() != 0 {
				t.Fatalf("exit=%d creates=%d uploads=%v output=%s", code, f.creates, f.uploads, out)
			}
		})
	}
}

func TestSitesPlannedStageDoesNotRetryOrPromoteAfterUncertainMutations(t *testing.T) {
	for _, mode := range []string{"create_failure", "wrong_creation", "after_create", "wrong_grants", "credential_header", "upload_failure", "finalize_failure", "wrong_finalize", "after_finalize"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlannedStageCLI(t)
			f.mode = mode
			out := planCLIOutput(t)
			if code := main.CmdSites(f.args()); code == 0 || f.creates != 1 || f.finalizes > 1 || out.Len() != 0 {
				t.Fatalf("exit=%d creates=%d finalizes=%d output=%s", code, f.creates, f.finalizes, out)
			}
			if mode == "after_create" || mode == "wrong_grants" || mode == "credential_header" || mode == "wrong_creation" {
				if len(f.uploads) != 0 || f.finalizes != 0 {
					t.Fatal("uploaded or finalized after a rejected stage")
				}
			}
		})
	}
}

func TestSitesPlannedStageConfigurationAndProvenanceOptIns(t *testing.T) {
	f := newPlannedStageCLI(t)
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "1")
	t.Setenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED", "1")
	f.caps.ConfigEnabled, f.caps.HistoryEnabled = true, true
	if err := os.WriteFile(filepath.Join(f.dir, hosting.ReleaseConfigFilename), []byte(`{"version":1,"headers":[],"redirects":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.regeneratePlan(t)
	args := append(f.args(), "--source-repository", "org/site", "--source-commit", strings.Repeat("a", 40))
	if code := main.CmdSites(args); code != 0 || f.release.Provenance == nil || f.release.Provenance.Repository != "org/site" {
		t.Fatalf("stage exit=%d provenance=%+v", code, f.release.Provenance)
	}
}

func TestReviewedSnapshotRejectsChangedConfiguration(t *testing.T) {
	f := newPlannedStageCLI(t)
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "1")
	f.caps.ConfigEnabled = true
	config := filepath.Join(f.dir, hosting.ReleaseConfigFilename)
	if err := os.WriteFile(config, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.regeneratePlan(t)
	if err := os.WriteFile(config, []byte(`{"version":1,"redirects":[{"from":"/old","to":"/new","status":308}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdSites(f.args()); code == 0 || len(f.requests) != 0 {
		t.Fatal("changed configuration reached the API")
	}
}

func TestPlannedStageHelpDescribesSavedPlan(t *testing.T) {
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	out := planCLIOutput(t)
	if code := main.Run([]string{"help", "sites", "deploy"}); code != 0 || !strings.Contains(out.String(), "--plan") || !strings.Contains(out.String(), "--no-promote") || !strings.Contains(out.String(), "--resume-release") {
		t.Fatal(fmt.Sprintf("help exit=%d: %s", code, out))
	}
}
