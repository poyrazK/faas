// adr: 686, 682, 20261019195000
package conformance

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func testImageRuntimeProfile(t *testing.T, fx *Fixture) {
	t.Helper()
	accepted, err := frameworkprofile.CaptureImageRuntime(nil, &api.ComposeHealthcheck{Test: []string{"NONE"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:runtime-profile", Status: state.DeployPending,
		InferredProfile: accepted,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := []byte(`{"version":"v1","framework":"unknown","port":5678}`)
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, dep.ID, profile); err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.DeploymentByID(fx.Ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(stored.InferredProfile, &got); err != nil || got.Port != 5678 || stored.OverridePort != 0 {
		t.Fatalf("runtime profile=%s override_port=%d err=%v", stored.InferredProfile, stored.OverridePort, err)
	}
	check, err := frameworkprofile.ImageHealthcheckFromProfile(stored.InferredProfile)
	if err != nil || check == nil || check.Override == nil || len(check.Override.Test) != 1 || check.Override.Test[0] != "NONE" {
		t.Fatalf("runtime profile lost its accepted healthcheck: %s, %v", stored.InferredProfile, err)
	}
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, dep.ID, []byte(`{bad`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
	if err := fx.Store.UpdateDeploymentStatus(fx.Ctx, dep.ID, state.DeployCancelled, ""); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, dep.ID, profile); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("terminal profile write error=%v, want invalid state transition", err)
	}
}

// A built service that froze a Compose healthcheck records imaged's readiness
// verdict through the same writer; other source deployments cannot, and the
// frozen override survives the write (ADR-20261019195000).
func testSourceRuntimeProfileHealthcheck(t *testing.T, fx *Fixture) {
	t.Helper()
	plain, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging,
		InferredProfile: []byte(`{"version":"v1","framework":"docker"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	verdict := []byte(`{"version":"v1","framework":"docker","image_healthcheck_required":true}`)
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, plain.ID, verdict); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("source deployment without a frozen healthcheck: error=%v, want invalid state transition", err)
	}
	frozen, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "docker",
		ImageHealthcheck: &frameworkprofile.ImageHealthcheck{Version: frameworkprofile.ImageHealthcheckVersion,
			Override: &api.ComposeHealthcheck{Test: []string{"CMD", "/check"}}}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging, InferredProfile: frozen,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetDeploymentRuntimeProfile(fx.Ctx, dep.ID, verdict); err != nil {
		t.Fatal(err)
	}
	stored, err := fx.Store.DeploymentByID(fx.Ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !frameworkprofile.RequiresSourceImageHealthcheck(stored.InferredProfile) {
		t.Fatalf("verdict not recorded: %s", stored.InferredProfile)
	}
	check, err := frameworkprofile.ImageHealthcheckFromProfile(stored.InferredProfile)
	if err != nil || check == nil || check.Override == nil || len(check.Override.Test) != 2 || check.Override.Test[1] != "/check" {
		t.Fatalf("frozen Compose healthcheck lost: %s, %v", stored.InferredProfile, err)
	}
}
