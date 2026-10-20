package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func TestSitesDiagnoseOnlyReadsAndComparesOriginalManifest(t *testing.T) {
	// adr: 202610171059 — report production is not a health assertion or a retry action.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("static"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := main.StaticHostingManifest(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Method != "GET" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("diagnostic mutation", r.Method)
		}
		if strings.HasSuffix(r.URL.Path, "/capabilities") {
			_ = json.NewEncoder(w).Encode(api.HostingCapabilities{DiagnosticsEnabled: true})
			return
		}
		if r.URL.Path != "/v1/internal/hosting/sites/"+reviewSiteID+"/diagnostics" || r.URL.Query().Get("run_id") != reviewReleaseID {
			t.Error("wrong diagnostic scope", r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.HostingDiagnostics{SiteID: reviewSiteID, RunID: reviewReleaseID, ServingHealth: "not_checked", RetryAdvice: "retry_same_run", ManifestFingerprint: hosting.HostingManifestFingerprint(manifest), Checks: []api.HostingDiagnosticCheck{}})
	})
	t.Setenv("FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED", "1")
	var captured bytes.Buffer
	old := (*main.OsStdout)
	(*main.OsStdout) = &captured
	t.Cleanup(func() { (*main.OsStdout) = old })
	args := []string{"diagnose", "--site", reviewSiteID, "--run-id", reviewReleaseID, "--path", dir}
	if code := main.CmdSites(args); code != 0 {
		t.Fatal(code)
	}
	var report api.HostingDiagnostics
	if err = json.Unmarshal(captured.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.RetryAdvice != "retry_same_run" || report.Checks[0].Code != "manifest_matches" {
		t.Fatal(report)
	}
	if err = os.WriteFile(filepath.Join(dir, "index.html"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	captured.Reset()
	if code := main.CmdSites(args); code != 0 {
		t.Fatal("blocked report should still be printable", code)
	}
	if err = json.Unmarshal(captured.Bytes(), &report); err != nil || report.RetryAdvice != "inspect_before_retry" || report.Checks[0].Code != "manifest_changed" {
		t.Fatal(report, err)
	}
	if len(paths) != 4 {
		t.Fatal("unexpected requests", paths)
	}
	for _, private := range []string{"operator-token", dir, "index.html"} {
		if strings.Contains(captured.String(), private) {
			t.Fatal("private material in report", private)
		}
	}
}

func TestSitesDiagnoseDisabledAndInvalidInputsNeverReachAPI(t *testing.T) {
	calls := 0
	reviewCLI(t, func(http.ResponseWriter, *http.Request) { calls++ })
	t.Setenv("FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED", "")
	if main.CmdSites([]string{"diagnose", "--site", reviewSiteID}) == 0 {
		t.Fatal("default-off ignored")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED", "1")
	for _, args := range [][]string{{"--site", "bad"}, {"--site", reviewSiteID, "--run-id", "bad"}, {"--site", reviewSiteID, "--path", "dist"}, {"--site", reviewSiteID, "--spa"}} {
		if main.CmdSitesDiagnose(args) == 0 {
			t.Fatal("invalid input accepted", args)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input made a network request", calls)
	}
}

func TestSitesDiagnoseManifestMatchNeverClearsOtherBlockers(t *testing.T) {
	manifest := api.HostingManifest{}
	for _, fingerprint := range []string{"", hosting.HostingManifestFingerprint(manifest)} {
		report := api.HostingDiagnostics{RetryAdvice: "inspect_before_retry", ManifestFingerprint: fingerprint}
		main.CompareDiagnosticManifest(&report, manifest)
		if report.RetryAdvice != "inspect_before_retry" {
			t.Fatal("local match cleared expiry or revocation blocker")
		}
	}
}
