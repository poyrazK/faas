// adr: 682
package imaged

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFrozenImageHealthcheckIgnoresLaterAppConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check *api.ComposeHealthcheck
		want  []string
	}{
		{"inherit", nil, []string{"CMD", "/image-check"}},
		{"replace", &api.ComposeHealthcheck{Test: []string{"CMD", "/accepted-check"}}, []string{"CMD", "/accepted-check"}},
		{"disabled", &api.ComposeHealthcheck{Test: []string{"NONE"}}, []string{"NONE"}},
		{"partial", &api.ComposeHealthcheck{TimeoutNS: int64(250 * time.Millisecond)}, []string{"CMD", "/image-check"}},
	} {
		for _, sourceTransition := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/edited image", true: "/source transition"}[sourceTransition], func(t *testing.T) {
				profile, err := frameworkprofile.CaptureImageRuntime(nil, tc.check)
				if err != nil {
					t.Fatal(err)
				}
				app := state.App{Manifest: state.AppManifest{ProjectImage: "example.com/app:v2",
					ProjectImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"CMD", "/changed-check"}}}}
				if sourceTransition {
					app.Manifest.ProjectImage = ""
					app.Manifest.ProjectImageHealthcheck = nil
				}
				config := oci.ImageConfig{Cmd: []string{"/server"}, Healthcheck: &oci.ImageHealthcheck{
					Test: []string{"CMD", "/image-check"}, ImageTiming: &api.OCIHealthcheckTiming{TimeoutNS: int64(time.Second)}}}
				manifest, err := manifestFromImageConfigWithDeployment(config, app, state.Deployment{InferredProfile: profile})
				if err != nil || !reflect.DeepEqual(manifest.Healthcheck.Test, tc.want) {
					t.Fatalf("queued healthcheck = %+v, %v", manifest.Healthcheck, err)
				}
				wantTimeout := int64(time.Second)
				if tc.check != nil && tc.check.TimeoutNS > 0 {
					wantTimeout = tc.check.TimeoutNS
				}
				if manifest.Healthcheck.ImageTiming.TimeoutNS != wantTimeout {
					t.Fatalf("timeout = %d; want %d", manifest.Healthcheck.ImageTiming.TimeoutNS, wantTimeout)
				}
			})
		}
	}
}

func TestFrozenImageHealthcheckRejectsMalformedContract(t *testing.T) {
	for _, profile := range []string{
		`{"image_healthcheck":null}`,
		`{"image_healthcheck":{"version":"v2","override":null}}`,
		`{"image_healthcheck":{"version":"v1"}}`,
		`{"image_healthcheck":{"version":"v1","override":"bad"}}`,
		`{"image_healthcheck":{"version":"v1","override":{"test":["invalid"]}}}`,
		`{"image_healthcheck":{"version":"v1","override":{"interval_ns":-1}}}`,
	} {
		_, err := manifestFromImageConfigWithDeployment(oci.ImageConfig{Cmd: []string{"/server"}}, state.App{}, state.Deployment{InferredProfile: []byte(profile)})
		if err == nil {
			t.Fatalf("invalid contract accepted: %s", profile)
		}
	}
}
