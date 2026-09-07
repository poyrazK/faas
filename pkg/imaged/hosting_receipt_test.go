package imaged

import (
	"encoding/json"
	"testing"

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
