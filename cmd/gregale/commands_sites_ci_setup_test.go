package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting/cisetup"
)

func TestSitesCISetupExportsOnceAndPreflightsLocally(t *testing.T) {
	// adr: 202610171119 — actual workflow and preparation sources never leave the workstation.
	dir := t.TempDir()
	site := api.HostingSite{ID: reviewSiteID, AccountID: "account", DurableRouting: true, Status: "active"}
	tr := api.HostingOIDCTrust{ID: reviewReleaseID, SiteID: site.ID, AccountID: site.AccountID, HostingOIDCTrustRules: api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push"}}
	recipe := main.HostingCISetupRecipe{SiteID: site.ID, TrustID: tr.ID, Config: api.HostingCISetupConfig{APIBase: "https://api.gregale.dev", ActionCommit: strings.Repeat("a", 40), CheckoutCommit: strings.Repeat("b", 40), PrepareAction: "./.github/actions/prepare", CLIPath: "./bin/gregale", CLISHA256: strings.Repeat("c", 64), OutputPath: "dist", PreviewTTLSeconds: 3600, WaitTimeoutSeconds: 120}}
	raw, _ := json.Marshal(recipe)
	config := filepath.Join(dir, "setup.json")
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		if r.Method != "POST" || r.URL.Path != "/v1/internal/hosting/sites/"+site.ID+"/ci-setup" || strings.Contains(body.String(), "workflow_yaml") || strings.Contains(body.String(), "LOCAL_SECRET") {
			t.Error("unexpected setup request", r.Method, r.URL.Path)
		}
		var req api.HostingCISetupRequest
		if json.Unmarshal(body.Bytes(), &req) != nil {
			t.Error("bad config")
		}
		out := cisetup.Prepare(site, tr, api.HostingCapabilities{CISetupEnabled: true, OIDCEnabled: true, AutomationEnabled: true, HistoryEnabled: true, PreviewsEnabled: true, StagedReleasesSupported: true}, req, time.Now().UTC())
		_ = json.NewEncoder(w).Encode(out)
	})
	var output bytes.Buffer
	old := (*main.OsStdout)
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdout) = old })
	args := []string{"ci-setup", "--config", config, "--out", filepath.Join(dir, "review.yml")}
	t.Setenv("FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED", "")
	if main.CmdSites(args) == 0 || calls != 0 {
		t.Fatal("default off")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED", "1")
	if main.CmdSites(args) != 0 || calls != 1 {
		t.Fatal("export", output.String())
	}
	if main.CmdSites(args) == 0 || calls != 2 {
		t.Fatal("overwrote export")
	}
	workflow, _ := os.ReadFile(filepath.Join(dir, "review.yml"))
	stat, _ := os.Stat(filepath.Join(dir, "review.yml"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal(stat.Mode())
	}
	workflowDir := filepath.Join(dir, ".github/workflows")
	prepDir := filepath.Join(dir, ".github/actions/prepare")
	if err := os.MkdirAll(workflowDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(prepDir, 0700); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(workflowDir, "preview.yml")
	if err := os.WriteFile(filename, workflow, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepDir, "action.yml"), []byte("name: prep\nruns:\n using: composite\n steps:\n - shell: bash\n   run: echo LOCAL_SECRET\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args = []string{"ci-preflight", "--config", config, "--repo-root", dir}
	output.Reset()
	if main.CmdSites(args) != 0 {
		t.Fatal("preflight", output.String())
	}
	if err := os.WriteFile(filename, []byte(strings.Replace(string(workflow), "id-token: write", "id-token: read", 1)+"# LOCAL_SECRET\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if main.CmdSites(args) == 0 || strings.Contains(output.String(), "LOCAL_SECRET") {
		t.Fatal("drift/secret", output.String())
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.yml")
	if err := os.WriteFile(external, workflow, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filename); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if main.CmdSites(args) == 0 {
		t.Fatal("symlink escaped repository")
	}
}
func TestSitesCISetupRejectsMalformedRecipesBeforeNetworking(t *testing.T) {
	calls := 0
	reviewCLI(t, func(http.ResponseWriter, *http.Request) { calls++ })
	t.Setenv("FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED", "1")
	config := filepath.Join(t.TempDir(), "recipe.json")
	for _, raw := range []string{`{}`, `{"site_id":"bad"}`, `{"site_id":"` + reviewSiteID + `","trust_id":"` + reviewReleaseID + `","unknown":"PRIVATE_SECRET"}`, `{} {}`, strings.Repeat("x", api.HostingCISetupRequestMaxBytes+1)} {
		if err := os.WriteFile(config, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if main.CmdSites([]string{"ci-setup", "--config", config, "--out", config + ".yml"}) == 0 || calls != 0 {
			t.Fatal("malformed recipe reached API")
		}
	}
}
