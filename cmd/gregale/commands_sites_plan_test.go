package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func planCLIFixture(t *testing.T) (string, api.HostingSite, api.HostingCapabilities, api.HostingUsage) {
	t.Helper()
	t.Setenv("FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	site := api.HostingSite{ID: reviewSiteID, AccountID: "33333333-3333-4333-8333-333333333333", Status: "active", Revision: 7}
	caps := api.HostingCapabilities{FilesMax: api.HostingFilesMax, FileMaxBytes: api.HostingFileMaxBytes, ReleaseMaxBytes: api.HostingReleaseMaxBytes, ManifestMaxBytes: api.HostingManifestMaxBytes,
		ReleasesPerSite: api.HostingReleasesPerSite, AccountStorageMaxBytes: api.HostingAccountStorageMaxBytes, AccountPendingUploadsMax: api.HostingAccountPendingUploadsMax}
	usage := api.HostingUsage{AccountID: site.AccountID, StorageLimitBytes: api.HostingAccountStorageMaxBytes, PendingUploadsLimit: api.HostingAccountPendingUploadsMax, ObservedAt: time.Now().UTC()}
	return dir, site, caps, usage
}

func planCLIOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previous := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previous })
	return &out
}

func planCLIResponse(t *testing.T, w http.ResponseWriter, r *http.Request, site api.HostingSite, caps api.HostingCapabilities, usage api.HostingUsage) {
	t.Helper()
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		t.Errorf("planner sent unexpected request: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body any
	switch r.URL.Path {
	case "/v1/internal/hosting/capabilities":
		body = caps
	case "/v1/internal/hosting/sites/" + reviewSiteID:
		body = site
	case "/v1/internal/hosting/sites/" + reviewSiteID + "/releases":
		body = []api.HostingRelease{}
	case "/v1/internal/hosting/usage":
		body = usage
	default:
		t.Errorf("planner requested unexpected endpoint: %s", r.URL.Path)
		w.WriteHeader(404)
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Error(err)
	}
}

func TestSitesPlanReadOnlyJSONAndQuotaExit(t *testing.T) {
	// adr: 202610171338 — planning allocates nothing, including when quota is full.
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			dir, site, caps, usage := planCLIFixture(t)
			if blocked {
				usage.ReservedBytes = api.HostingAccountStorageMaxBytes
			}
			var calls []string
			reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				planCLIResponse(t, w, r, site, caps, usage)
			})
			out := planCLIOutput(t)
			code := main.CmdSites([]string{"plan", "--site", reviewSiteID, "--path", dir, "--spa"})
			wantCode := 0
			if blocked {
				wantCode = 2
			}
			wantCalls := []string{"/v1/internal/hosting/capabilities", "/v1/internal/hosting/sites/" + reviewSiteID, "/v1/internal/hosting/sites/" + reviewSiteID + "/releases", "/v1/internal/hosting/usage", "/v1/internal/hosting/sites/" + reviewSiteID}
			if code != wantCode || !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("exit=%d requests=%v", code, calls)
			}
			var plan api.HostingDeploymentPlan
			if err := json.Unmarshal(out.Bytes(), &plan); err != nil || plan.Version != api.HostingDeploymentPlanVersion || plan.APIOrigin != main.ApiBase() || !plan.Advisory || plan.ExpectedRevision != 7 || plan.Changes.Added != 1 || !plan.Changes.AfterSettings.SPAFallback || plan.FileCount != 1 || plan.PayloadBytes != 5 || plan.AdditionalReservedBytes != 10+api.HostingManifestMaxBytes || len(plan.ManifestFingerprint) != 64 || plan.StartedAt.IsZero() || plan.ObservedAt.Before(plan.StartedAt) || !plan.UsageObservedAt.Equal(usage.ObservedAt) {
				t.Fatalf("plan %+v: %v", plan, err)
			}
			for _, secret := range []string{"operator-token", site.AccountID, "upload_url", "release_id\":\"" + reviewReleaseID} {
				if strings.Contains(out.String(), secret) {
					t.Fatalf("unexpected inventory/credential: %q", secret)
				}
			}
		})
	}
}

func TestSitesPlanGatesAndLocalValidationBeforeRequests(t *testing.T) {
	dir, _, _, _ := planCLIFixture(t)
	calls := 0
	reviewCLI(t, func(http.ResponseWriter, *http.Request) { calls++ })
	args := []string{"plan", "--site", reviewSiteID, "--path", dir}
	for _, value := range []string{"", "false", "TRUE"} {
		t.Setenv("FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED", value)
		if main.CmdSites(args) == 0 {
			t.Fatalf("accepted disabled plan flag %q", value)
		}
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_DEPLOYMENT_PLAN_ENABLED", "true")
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "")
	if main.CmdSites(args) == 0 {
		t.Fatal("main hosting gate bypassed")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	for _, invalid := range [][]string{
		{"plan", "--site", "bad", "--path", dir},
		{"plan", "--site", reviewSiteID, "--path", dir, "extra"},
		{"plan", "--site", reviewSiteID, "--path", dir, "--promote"},
		{"plan", "--site", reviewSiteID, "--path", t.TempDir()},
	} {
		if main.CmdSites(invalid) == 0 {
			t.Fatalf("accepted %v", invalid)
		}
	}
	if err := os.Symlink("index.html", filepath.Join(dir, "link.html")); err != nil {
		t.Fatal(err)
	}
	if main.CmdSites(args) == 0 {
		t.Fatal("planned symlinked build")
	}
	if calls != 0 {
		t.Fatalf("invalid input sent %d requests", calls)
	}
}

func TestSitesPlanDoesNotEmitPartialReportsOnReadFailureOrChangedSite(t *testing.T) {
	for failAt := 1; failAt <= 6; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			dir, site, caps, usage := planCLIFixture(t)
			calls := 0
			reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == failAt {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if failAt == 6 && calls == 5 {
					site.Revision++
				}
				planCLIResponse(t, w, r, site, caps, usage)
			})
			out := planCLIOutput(t)
			wantCode := 3 // Preserve the CLI's platform-failure exit code.
			if failAt == 6 {
				wantCode = 1
			}
			if code := main.CmdSites([]string{"plan", "--site", reviewSiteID, "--path", dir}); code != wantCode || out.Len() != 0 || calls > 5 {
				t.Fatalf("exit=%d calls=%d output=%s", code, calls, out)
			}
		})
	}
}

func TestSitesPlanPreservesAPIErrorExitsWithoutReport(t *testing.T) {
	for _, tc := range []struct{ status, exit int }{{401, 2}, {403, 6}, {404, 4}, {409, 5}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			dir, site, caps, usage := planCLIFixture(t)
			calls := 0
			reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/v1/internal/hosting/usage" {
					w.WriteHeader(tc.status)
					_ = json.NewEncoder(w).Encode(api.NewProblem(tc.status, "plan_evidence_unavailable", "Evidence unavailable", ""))
					return
				}
				planCLIResponse(t, w, r, site, caps, usage)
			})
			out := planCLIOutput(t)
			if code := main.CmdSites([]string{"plan", "--site", reviewSiteID, "--path", dir}); code != tc.exit || calls != 4 || out.Len() != 0 {
				t.Fatalf("exit=%d calls=%d output=%s", code, calls, out)
			}
		})
	}
}

func TestSitesPlanMissingInventoryIsNotAnEmptySite(t *testing.T) {
	for _, body := range []string{"", "null"} {
		t.Run(body, func(t *testing.T) {
			dir, site, caps, usage := planCLIFixture(t)
			calls := 0
			reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if strings.HasSuffix(r.URL.Path, "/releases") {
					_, _ = fmt.Fprint(w, body)
					return
				}
				planCLIResponse(t, w, r, site, caps, usage)
			})
			out := planCLIOutput(t)
			if code := main.CmdSites([]string{"plan", "--site", reviewSiteID, "--path", dir}); code != 1 || calls != 3 || out.Len() != 0 {
				t.Fatalf("exit=%d calls=%d output=%s", code, calls, out)
			}
		})
	}
}

func TestSitesPlanRejectsScopedCredentialsBeforeAccountRead(t *testing.T) {
	dir, site, caps, usage := planCLIFixture(t)
	caps.AutomationSiteID = site.ID
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/internal/hosting/capabilities" {
			t.Error("scoped token accessed account evidence")
		}
		planCLIResponse(t, w, r, site, caps, usage)
	})
	out := planCLIOutput(t)
	if code := main.CmdSites([]string{"plan", "--site", reviewSiteID, "--path", dir}); code != 1 || calls != 1 || out.Len() != 0 {
		t.Fatalf("exit=%d calls=%d output=%s", code, calls, out)
	}
}

func TestSitesPlanConfigFlagsRemainIndependent(t *testing.T) {
	dir, site, caps, usage := planCLIFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "gregale-hosting.json"), []byte(`{"version":1,"redirects":[{"from":"/old","to":"/new","status":308}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) { calls++; planCLIResponse(t, w, r, site, caps, usage) })
	out := planCLIOutput(t)
	args := []string{"plan", "--site", reviewSiteID, "--path", dir}
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "")
	if main.CmdSites(args) != 1 || calls != 0 {
		t.Fatal("local config flag bypassed")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED", "1")
	if main.CmdSites(args) != 2 || calls != 5 {
		t.Fatal("API config flag bypassed")
	}
	var plan api.HostingDeploymentPlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil || !reflect.DeepEqual(plan.Blockers, []string{"config_disabled"}) || plan.Changes.AfterSettings.Config.Redirects[0].To != "/new" {
		t.Fatalf("plan %+v: %v", plan, err)
	}
}

func TestSitesPlanRejectsOversizedSerializedManifestBeforeRequests(t *testing.T) {
	dir, _, _, _ := planCLIFixture(t)
	subdir := filepath.Join(dir, strings.Repeat("a", 240))
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < api.HostingFilesMax; i++ {
		name := filepath.Join(subdir, fmt.Sprintf("%04d-%s", i, strings.Repeat("b", 240)))
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	reviewCLI(t, func(http.ResponseWriter, *http.Request) { calls++ })
	if main.CmdSites([]string{"plan", "--site", reviewSiteID, "--path", dir}) != 1 || calls != 0 {
		t.Fatal("oversized manifest reached API")
	}
}
