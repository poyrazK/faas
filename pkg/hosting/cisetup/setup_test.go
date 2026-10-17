package cisetup

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

func setupFixture() (api.HostingSite, api.HostingOIDCTrust, api.HostingCapabilities, api.HostingCISetupRequest) {
	site := api.HostingSite{ID: "11111111-1111-4111-8111-111111111111", AccountID: "account", Status: "active", DurableRouting: true}
	trust := api.HostingOIDCTrust{ID: "22222222-2222-4222-8222-222222222222", SiteID: site.ID, AccountID: site.AccountID, HostingOIDCTrustRules: api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push", Environment: "pilot"}}
	caps := api.HostingCapabilities{CISetupEnabled: true, OIDCEnabled: true, AutomationEnabled: true, PreviewsEnabled: true, HistoryEnabled: true, StagedReleasesSupported: true}
	req := api.HostingCISetupRequest{TrustID: trust.ID, Config: api.HostingCISetupConfig{APIBase: "https://api.gregale.dev", ActionCommit: strings.Repeat("a", 40), CheckoutCommit: strings.Repeat("b", 40), CLISHA256: strings.Repeat("c", 64), CLIPath: "./.gregale/bin/gregale", PrepareAction: "./.github/actions/prepare-frontend", OutputPath: "dist", PreviewTTLSeconds: 3600, WaitTimeoutSeconds: 120}}
	return site, trust, caps, req
}
func TestPrepareBindsWorkflowToImmutableTrustAndPins(t *testing.T) {
	// adr: 202610171119 — export is read-only intent, never deployment readiness.
	for _, event := range []string{"push", "workflow_dispatch", "schedule"} {
		t.Run(event, func(t *testing.T) {
			site, trust, caps, req := setupFixture()
			trust.EventName = event
			if event == "schedule" {
				req.Config.Cron = "*/5 * * * *"
				req.Config.DefaultBranch = "main"
			}
			out := Prepare(site, trust, caps, req, time.Unix(100, 0).UTC())
			if !out.ConfigurationValid || out.WorkflowPath != ".github/workflows/preview.yml" {
				t.Fatal(out)
			}
			var w workflow
			if err := yaml.Unmarshal([]byte(out.WorkflowYAML), &w); err != nil {
				t.Fatal(err)
			}
			j := w.Jobs["frontend"]
			if len(w.Permissions) != 0 || len(w.On) != 1 || j.Permissions["id-token"] != "write" || j.Permissions["contents"] != "read" || len(j.Permissions) != 2 || j.Environment != "pilot" || len(j.Steps) != 4 {
				t.Fatal(w)
			}
			if !strings.Contains(j.If, "github.ref == 'refs/heads/main'") || !strings.Contains(j.If, "github.event_name == '"+event+"'") || !strings.Contains(j.If, "github.repository == 'org/site'") {
				t.Fatal(j.If)
			}
			last := j.Steps[3]
			if last.With["oidc-trust"] != trust.ID || last.With["site"] != site.ID || last.With["api-token"] != nil || last.With["run-id"] != nil {
				t.Fatal(last)
			}
			if j.Steps[0].Uses != "actions/checkout@"+req.Config.CheckoutCommit || j.Steps[0].With["persist-credentials"] != false || j.Steps[1].Uses != req.Config.PrepareAction || !strings.Contains(j.Steps[2].Run, "hashlib.sha256") || j.Steps[2].Env["GREGALE_CLI_SHA256"] != req.Config.CLISHA256 || !strings.HasSuffix(last.Uses, "@"+req.Config.ActionCommit) {
				t.Fatal(j.Steps)
			}
			if out.Checks[len(out.Checks)-1].State != "manual" || len(out.Recovery) < 3 {
				t.Fatal(out)
			}
		})
	}
}
func TestPrepareRejectsIncompatibleAndUnresolvedSettings(t *testing.T) {
	tests := map[string]func(*api.HostingSite, *api.HostingOIDCTrust, *api.HostingCapabilities, *api.HostingCISetupRequest){
		"suspended": func(s *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, _ *api.HostingCISetupRequest) {
			s.Suspended = true
		},
		"foreign trust": func(_ *api.HostingSite, t *api.HostingOIDCTrust, _ *api.HostingCapabilities, _ *api.HostingCISetupRequest) {
			t.AccountID = "other"
		},
		"missing history": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, c *api.HostingCapabilities, _ *api.HostingCISetupRequest) {
			c.HistoryEnabled = false
		},
		"default off": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, c *api.HostingCapabilities, _ *api.HostingCISetupRequest) {
			c.CISetupEnabled = false
		},
		"oversized workflow": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			r.Config.APIBase = "https://" + strings.Repeat("a", api.HostingCISetupWorkflowMaxBytes)
		},
		"mutable pin": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			r.Config.ActionCommit = "main"
		},
		"placeholder": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			r.Config.OutputPath = "YOUR_OUTPUT"
		},
		"traversal": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			r.Config.PrepareAction = "./.github/actions/../bad"
		},
		"shell": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			r.Config.CLIPath = "./bin/$(echo bad)"
		},
		"env expression": func(_ *api.HostingSite, t *api.HostingOIDCTrust, _ *api.HostingCapabilities, _ *api.HostingCISetupRequest) {
			t.Environment = "${{ secrets.TOKEN }}"
		},
		"branch glob": func(_ *api.HostingSite, t *api.HostingOIDCTrust, _ *api.HostingCapabilities, _ *api.HostingCISetupRequest) {
			t.Ref = "refs/heads/main+"
			t.WorkflowRef = "org/site/.github/workflows/preview.yml@" + t.Ref
		},
		"pull request": func(_ *api.HostingSite, t *api.HostingOIDCTrust, _ *api.HostingCapabilities, _ *api.HostingCISetupRequest) {
			t.EventName = "pull_request_target"
		},
		"schedule mismatch": func(_ *api.HostingSite, t *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			t.EventName = "schedule"
			r.Config.Cron = "*/5 * * * *"
			r.Config.DefaultBranch = "other"
		},
		"schedule cadence": func(_ *api.HostingSite, t *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			t.EventName = "schedule"
			r.Config.Cron = "* * * * *"
			r.Config.DefaultBranch = "main"
		},
		"schedule mixed minute": func(_ *api.HostingSite, t *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			t.EventName = "schedule"
			r.Config.Cron = "*/5,6 * * * *"
			r.Config.DefaultBranch = "main"
		},
		"unexpected schedule": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			r.Config.Cron = "0 * * * *"
		},
		"ttl": func(_ *api.HostingSite, _ *api.HostingOIDCTrust, _ *api.HostingCapabilities, r *api.HostingCISetupRequest) {
			r.Config.PreviewTTLSeconds = 0
		},
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			s, tr, c, r := setupFixture()
			edit(&s, &tr, &c, &r)
			out := Prepare(s, tr, c, r, time.Now())
			if out.ConfigurationValid || out.WorkflowYAML != "" {
				t.Fatal(out)
			}
		})
	}
	for _, origin := range []string{"http://api.gregale.dev", "https://user:secret@api.gregale.dev", "https://api.gregale.dev/path", "https://api.gregale.dev?", "https://api.gregale.dev#", "https://YOUR_API"} {
		if validOrigin(origin) {
			t.Fatal(origin)
		}
	}
}
func TestWorkflowPreflightRejectsExecutableDriftAndAmbiguousYAML(t *testing.T) {
	s, tr, c, r := setupFixture()
	expected := Prepare(s, tr, c, r, time.Now()).WorkflowYAML
	if !WorkflowMatches("# extra review comment\n"+expected, expected) {
		t.Fatal("comments must be accepted")
	}
	for name, raw := range map[string]string{
		"permissions":        strings.Replace(expected, "contents: read", "contents: write", 1),
		"mutable checkout":   strings.Replace(expected, "actions/checkout@"+r.Config.CheckoutCommit, "actions/checkout@main", 1),
		"wrong trust":        strings.ReplaceAll(expected, tr.ID, s.ID),
		"missing oidc":       strings.Replace(expected, "id-token: write", "id-token: read", 1),
		"duplicate":          expected + "name: stolen\n",
		"multiple documents": expected + "---\nname: other\n",
		"aliases":            "a: &value hi\nb: *value\n",
		"custom mapping":     "a: !secret {b: hi}\n",
		"custom sequence":    "a: !secret [hi]\n",
		"custom tags":        "a: !secret hi\n",
		"oversized":          strings.Repeat("x", api.HostingCISetupWorkflowMaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if WorkflowMatches(raw, expected) {
				t.Fatal("accepted drift")
			}
		})
	}
	if !ValidPreparationAction("name: prepare\nruns:\n  using: composite\n  steps:\n    - shell: bash\n      run: echo prepare\n") || ValidPreparationAction("runs:\n using: node24\n main: action.js\n") || ValidPreparationAction("runs:\n using: composite\n steps: []\n") {
		t.Fatal("composite shape")
	}
}

func TestPreparePreviewReservationsAreExplicitAndGated(t *testing.T) {
	// adr: 202610171216 — generated workflows never silently change existing retry fingerprints.
	site, trust, caps, req := setupFixture()
	req.Config.ReservePreview = true
	if out := Prepare(site, trust, caps, req, time.Now().UTC()); out.ConfigurationValid || out.WorkflowYAML != "" {
		t.Fatal("disabled reservation export", out)
	}
	caps.PreviewReservationsEnabled = true
	out := Prepare(site, trust, caps, req, time.Now().UTC())
	if !out.ConfigurationValid || !strings.Contains(out.WorkflowYAML, `reserve-preview: "true"`) {
		t.Fatal(out)
	}
}
