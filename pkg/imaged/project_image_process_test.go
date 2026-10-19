// adr: 1337
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

func TestProjectImageProcessOverridesOCIConfig(t *testing.T) {
	grace := 25
	entry := func(args ...string) *[]string {
		if args == nil {
			args = []string{}
		}
		return &args
	}
	for _, tc := range []struct {
		name     string
		command  []string
		process  *api.ComposeProcess
		argv     []string
		user     string
		workdir  string
		signal   string
		graceDur time.Duration
	}{
		{name: "inherited", argv: []string{"/entrypoint", "original"}, user: "app", workdir: "/image"},
		{name: "entrypoint drops image CMD", process: &api.ComposeProcess{Entrypoint: entry("tini", "--")},
			argv: []string{"tini", "--"}, user: "app", workdir: "/image"},
		{name: "entrypoint with command", command: []string{"node", "server.js"}, process: &api.ComposeProcess{Entrypoint: entry("tini", "--")},
			argv: []string{"tini", "--", "node", "server.js"}, user: "app", workdir: "/image"},
		{name: "cleared entrypoint runs command", command: []string{"node", "server.js"}, process: &api.ComposeProcess{Entrypoint: entry()},
			argv: []string{"node", "server.js"}, user: "app", workdir: "/image"},
		{name: "identity and stop", process: &api.ComposeProcess{User: "1000:1000", WorkingDir: "/srv", StopSignal: "SIGINT", StopGracePeriodS: &grace},
			argv: []string{"/entrypoint", "original"}, user: "1000:1000", workdir: "/srv", signal: "SIGINT", graceDur: 25 * time.Second},
	} {
		for _, frozen := range []bool{true, false} {
			name := tc.name + map[bool]string{true: " (frozen)", false: " (app)"}[frozen]
			t.Run(name, func(t *testing.T) {
				config := oci.ImageConfig{Entrypoint: []string{"/entrypoint"}, Cmd: []string{"original"}, User: "app", WorkingDir: "/image"}
				app := state.App{Manifest: state.AppManifest{ProjectImage: "example.com/app:v1",
					ProjectImageCommand: tc.command, ProjectImageProcess: tc.process}}
				var dep state.Deployment
				if frozen {
					profile, err := frameworkprofile.CaptureImageRuntime(tc.command, nil, tc.process)
					if err != nil {
						t.Fatal(err)
					}
					dep.InferredProfile = profile
					// The frozen contract wins over later app edits.
					app.Manifest.ProjectImageCommand = []string{"edited"}
					app.Manifest.ProjectImageProcess = &api.ComposeProcess{User: "edited"}
				}
				manifest, err := manifestFromImageConfigWithDeployment(config, app, dep)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(manifest.Entrypoint, tc.argv) || manifest.User != tc.user || manifest.WorkingDir != tc.workdir ||
					manifest.StopSignal != tc.signal || manifest.StopGracePeriod != tc.graceDur {
					t.Fatalf("manifest argv=%v user=%q dir=%q signal=%q grace=%s; want %v %q %q %q %s", manifest.Entrypoint,
						manifest.User, manifest.WorkingDir, manifest.StopSignal, manifest.StopGracePeriod,
						tc.argv, tc.user, tc.workdir, tc.signal, tc.graceDur)
				}
				if config.Cmd[0] != "original" || config.Entrypoint[0] != "/entrypoint" {
					t.Fatal("mutated the image defaults")
				}
			})
		}
	}
}

func TestProjectImageProcessFrozenV2FailsClosedWhenUndecodable(t *testing.T) {
	dep := state.Deployment{InferredProfile: []byte(`{"image_command":{"version":"v9","cmd":null,"process":{"user":"app"}}}`)}
	app := state.App{Manifest: state.AppManifest{ProjectImage: "example.com/app:v1"}}
	if _, err := manifestFromImageConfigWithDeployment(oci.ImageConfig{Cmd: []string{"run"}}, app, dep); err == nil {
		t.Fatal("an unknown image command contract started the image's own process")
	}
}
