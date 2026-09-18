package imaged

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuildHostingReceiptUsesPersistedProfileAndURL(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "apps.example.test")
	raw, err := json.Marshal(frameworkprofile.Profile{
		Version: frameworkprofile.Version, Framework: "hono", FrameworkVer: "4.7.0",
		StartCommand: "npm run start", Port: 8787, HealthPath: "/ready", Inferred: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := buildHostingReceipt(
		state.App{ID: "app-1", Slug: "demo"},
		state.Deployment{ID: "dep-1", Kind: state.DeploymentKindTarball, InferredProfile: raw},
		apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeSkipped},
	)
	if receipt.AppURL != "https://demo.apps.example.test" {
		t.Fatalf("app_url = %q, want hosted URL", receipt.AppURL)
	}
	if receipt.Profile.Framework != "hono" || receipt.Profile.StartCommand != "npm run start" || receipt.Profile.Port != 8787 || receipt.Profile.HealthPath != "/ready" {
		t.Fatalf("profile = %+v, want persisted profile", receipt.Profile)
	}
}

func TestHostingAppURLEmptySlug(t *testing.T) {
	if got := hostingAppURL(""); got != "" {
		t.Fatalf("hostingAppURL(\"\") = %q, want empty", got)
	}
}

func TestHostingReceiptProfileDirectOCIUsesTCPReadiness(t *testing.T) {
	profile := hostingReceiptProfile(
		state.App{Manifest: state.AppManifest{Healthz: defaultHealthzPath}},
		state.Deployment{Kind: state.DeploymentKindImage},
	)
	if profile.HealthPath != "" {
		t.Fatalf("direct OCI health path = %q, want empty TCP-readiness contract", profile.HealthPath)
	}
	if profile.Port != api.DefaultAppPort {
		t.Fatalf("direct OCI port = %d, want %d", profile.Port, api.DefaultAppPort)
	}
}

func TestHostingReceiptProfileDirectOCINonDefaultManifestHealthWins(t *testing.T) {
	profile := hostingReceiptProfile(
		state.App{Manifest: state.AppManifest{Healthz: "/ready"}},
		state.Deployment{Kind: state.DeploymentKindImage},
	)
	if profile.HealthPath != "/ready" {
		t.Fatalf("direct OCI manifest health path = %q, want /ready", profile.HealthPath)
	}
}

func TestHostingReceiptProfileFunctionImageKeepsHTTPReadiness(t *testing.T) {
	profile := hostingReceiptProfile(
		state.App{Type: state.AppTypeFunction, Manifest: state.AppManifest{Healthz: defaultHealthzPath}},
		state.Deployment{Kind: state.DeploymentKindImage},
	)
	if profile.HealthPath != defaultHealthzPath {
		t.Fatalf("function image health path = %q, want %s", profile.HealthPath, defaultHealthzPath)
	}
}

func TestHostingReceiptProfileDirectOCIHealthOverrideWins(t *testing.T) {
	profile := hostingReceiptProfile(
		state.App{},
		state.Deployment{
			Kind:                state.DeploymentKindImage,
			OverrideHealthcheck: json.RawMessage(`{"path":"/ready"}`),
		},
	)
	if profile.HealthPath != "/ready" {
		t.Fatalf("direct OCI override health path = %q, want /ready", profile.HealthPath)
	}
}

func TestHostingHealthPathDirectOCIFallsBackToRootForSmoke(t *testing.T) {
	path := HostingHealthPath(state.App{}, state.Deployment{Kind: state.DeploymentKindImage})
	if path != "/" {
		t.Fatalf("direct OCI smoke path = %q, want /", path)
	}
}
