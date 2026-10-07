// adr: 680, 682
package imaged

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// The general manifest-puller fixture historically folds ENTRYPOINT into CMD.
// This fixture exposes both OCI fields to test Compose replacement semantics.
type frozenCommandPuller struct{ *resolvingTestPuller }

func (p *frozenCommandPuller) PullImageConfig(ctx context.Context, ref string) (oci.ImageConfig, error) {
	if _, err := p.resolvingTestPuller.PullImageConfig(ctx, ref); err != nil {
		return oci.ImageConfig{}, err
	}
	return oci.ImageConfig{Entrypoint: p.appConfig.Entrypoint, Cmd: p.appConfig.Cmd, Healthcheck: p.appConfig.Healthcheck}, nil
}

func TestFrozenImageCommandIgnoresLaterAppConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command []string
		want    []string
	}{
		{"inherit OCI CMD", nil, []string{"/entrypoint", "original"}},
		{"replace OCI CMD", []string{"serve", "argument with spaces"}, []string{"/entrypoint", "serve", "argument with spaces"}},
		{"shell CMD", []string{"/bin/sh", "-c", "exec /worker"}, []string{"/entrypoint", "/bin/sh", "-c", "exec /worker"}},
		{"empty CMD", []string{}, []string{"/entrypoint"}},
	} {
		for _, sourceTransition := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/edited image", true: "/source transition"}[sourceTransition], func(t *testing.T) {
				profile, err := frameworkprofile.CaptureImageCommand(tc.command)
				if err != nil {
					t.Fatal(err)
				}
				dep := state.Deployment{Kind: state.DeploymentKindImage, InferredProfile: profile}
				app := state.App{StartCommand: "must not replace frozen image entrypoint", Manifest: state.AppManifest{
					ProjectImage: "example.com/app:v2", ProjectImageCommand: []string{"new command"}}}
				if sourceTransition {
					app.Manifest.ProjectImage = ""
					app.Manifest.ProjectImageCommand = nil
				}
				config := oci.ImageConfig{Entrypoint: []string{"/entrypoint"}, Cmd: []string{"original"}}
				manifest, err := manifestFromImageConfigWithDeployment(config, app, dep)
				if err != nil || !reflect.DeepEqual(manifest.Entrypoint, tc.want) {
					t.Fatalf("argv = %v, %v; want %v", manifest.Entrypoint, err, tc.want)
				}
				if config.Cmd[0] != "original" {
					t.Fatal("changed OCI artifact defaults")
				}
				// Explicit entrypoint/cmd deployment overrides retain their
				// precedence after the captured Compose contract is applied.
				manifest, err = applyOverrides(manifest, state.Deployment{OverrideEntrypoint: []string{"/override"}, OverrideCmd: []string{"arg"}})
				if err != nil || !reflect.DeepEqual(manifest.Entrypoint, []string{"/override", "arg"}) {
					t.Fatalf("explicit overrides = %v, %v", manifest.Entrypoint, err)
				}
			})
		}
	}
}

func TestFrozenImageCommandRejectsUnknownOrMalformedContract(t *testing.T) {
	for _, profile := range []string{
		`{"image_command":{"version":"v2","cmd":null}}`,
		`{"image_command":{"version":"v1"}}`,
		`{"image_command":{"version":"v1","cmd":"wrong shape"}}`,
		`{"image_command":null}`,
	} {
		_, err := manifestFromImageConfigWithDeployment(oci.ImageConfig{Cmd: []string{"original"}}, state.App{},
			state.Deployment{InferredProfile: []byte(profile)})
		if err == nil {
			t.Fatalf("fell back to mutable app configuration for %s", profile)
		}
	}
}
