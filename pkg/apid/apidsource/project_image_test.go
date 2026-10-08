// adr: 678, 686, 682
package apidsource

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectImageEnqueueWithoutSourceBuild(t *testing.T) {
	for _, image := range []string{"docker.io/library/nginx:1.27", "ghcr.io/example/app@sha256:" + strings.Repeat("a", 64)} {
		t.Run(image, func(t *testing.T) {
			store := state.NewMemStore()
			app := mustSeedApp(t, store)
			notifier := &recordingNotifier{}
			p := EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindTarball,
				ImageRef: image, ImagePort: 80, FullRootfsAllowAuto: true,
				ImageCommand:     []string{"serve", "with spaces"},
				ImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"CMD", "/check"}, TimeoutNS: 250000000},
				SourcePath:       "/does/not/exist.tar.gz", SourceBytes: 99,
				Scope: "staging", ActorUserID: app.AccountID, ActorVia: "cli",
				LogSpool: t.TempDir(), Log: quietLogger(), ReleaseCommand: []string{"/app/migrate"}}
			result, err := Enqueue(t.Context(), store, notifier, p)
			if err != nil || result.DeploymentID == "" || result.BuildID != "" {
				t.Fatalf("enqueue = %+v, %v", result, err)
			}
			dep, err := store.DeploymentByID(t.Context(), result.DeploymentID)
			if err != nil || dep.Kind != state.DeploymentKindImage || dep.ImageDigest != image || dep.OverridePort != 80 ||
				dep.Scope != "staging" || dep.DeployedVia != "cli" || !dep.FullRootfsAllowAuto || dep.SourcePath != "" ||
				strings.Join(dep.ReleaseCommand, ",") != "/app/migrate" {
				t.Fatalf("deployment = %+v, %v", dep, err)
			}
			captured, err := frameworkprofile.ImageCommandFromProfile(dep.InferredProfile)
			if err != nil || captured == nil || strings.Join(captured.Cmd, ",") != "serve,with spaces" {
				t.Fatalf("image command was not captured: %s, %v", dep.InferredProfile, err)
			}
			check, err := frameworkprofile.ImageHealthcheckFromProfile(dep.InferredProfile)
			if err != nil || check == nil || check.Override == nil || check.Override.Test[1] != "/check" || check.Override.TimeoutNS != 250000000 {
				t.Fatalf("image healthcheck was not captured: %s, %v", dep.InferredProfile, err)
			}
			if _, err := store.BuildByDeployment(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("image queued a source build: %v", err)
			}
			if len(notifier.calls) != 1 || notifier.calls[0].channel != db.NotifyDeploymentChanged {
				t.Fatalf("notifications = %+v", notifier.calls)
			}
			var payload map[string]string
			if err := json.Unmarshal([]byte(notifier.calls[0].payload), &payload); err != nil || payload["kind"] != "image" || payload["deployment_id"] != dep.ID {
				t.Fatalf("payload = %+v, %v", payload, err)
			}
		})
	}
}

func TestProjectImageWebhookDeliveryIsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	app := mustSeedApp(t, store)
	notifier := &recordingNotifier{}
	p := EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindGitHub, ImageRef: "ghcr.io/example/app:v1",
		DeliveryID: "image-delivery", SourcePath: "source.tar.gz", LogSpool: t.TempDir(), Log: quietLogger()}
	first, err := Enqueue(t.Context(), store, notifier, p)
	if err != nil {
		t.Fatal(err)
	}
	pinned := "ghcr.io/example/app@sha256:" + strings.Repeat("a", 64)
	if err := store.PinDeploymentImageReference(t.Context(), first.DeploymentID, p.ImageRef, pinned); err != nil {
		t.Fatal(err)
	}
	p.ImageCommand = []string{"changed since original delivery"}
	p.ImageHealthcheck = &api.ComposeHealthcheck{Test: []string{"NONE"}}
	second, err := Enqueue(t.Context(), store, notifier, p)
	if err != nil || first != second {
		t.Fatalf("delivery retry = %+v, %v; first = %+v", second, err, first)
	}
	dep, _ := store.DeploymentByID(t.Context(), first.DeploymentID)
	if dep.ImageDigest != pinned {
		t.Fatalf("delivery retry replaced immutable source: %+v", dep)
	}
	captured, err := frameworkprofile.ImageCommandFromProfile(dep.InferredProfile)
	if err != nil || captured == nil || captured.Cmd != nil {
		t.Fatalf("delivery retry replaced original inherited CMD: %s, %v", dep.InferredProfile, err)
	}
	check, err := frameworkprofile.ImageHealthcheckFromProfile(dep.InferredProfile)
	if err != nil || check == nil || check.Override != nil {
		t.Fatalf("delivery retry replaced original inherited healthcheck: %s, %v", dep.InferredProfile, err)
	}
}

func TestProjectImageHealthcheckRejectsBeforeCreation(t *testing.T) {
	store := state.NewMemStore()
	app := mustSeedApp(t, store)
	notifier := &recordingNotifier{}
	_, err := Enqueue(t.Context(), store, notifier, EnqueueParams{AppID: app.ID,
		Kind: state.DeploymentKindTarball, ImageRef: "example.com/app:v1", SourcePath: "source.tar.gz",
		ImageHealthcheck: &api.ComposeHealthcheck{TimeoutNS: -1}, LogSpool: t.TempDir(), Log: quietLogger()})
	if err == nil {
		t.Fatal("accepted malformed image healthcheck")
	}
	if _, err := store.LatestDeployment(t.Context(), app.ID); !errors.Is(err, state.ErrNotFound) || notifier.callCount() != 0 {
		t.Fatalf("invalid healthcheck left claimable work: %v, notifications=%d", err, notifier.callCount())
	}
}

func TestProjectImageSourceOperationsRejectBeforeCreation(t *testing.T) {
	store := state.NewMemStore()
	app := mustSeedApp(t, store)
	notifier := &recordingNotifier{}
	_, err := Enqueue(t.Context(), store, notifier, EnqueueParams{AppID: app.ID,
		Kind: state.DeploymentKindTarball, ImageRef: "example.com/app:v1", SourcePath: "source.tar.gz",
		LogSpool: t.TempDir(), Log: quietLogger(), OperationAdmissionEnabled: true,
		OperationDefinitions: []api.OperationDefinitionSpec{{Name: "export"}}})
	if err == nil {
		t.Fatal("accepted source operations without atomic image admission")
	}
	if _, err := store.LatestDeployment(t.Context(), app.ID); !errors.Is(err, state.ErrNotFound) || notifier.callCount() != 0 {
		t.Fatalf("rejected image left claimable work: %v, notifications=%d", err, notifier.callCount())
	}
}
