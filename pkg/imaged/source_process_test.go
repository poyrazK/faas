// adr: 20261022160000
package imaged

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceBuildAppliesComposeProcess(t *testing.T) {
	grace := 20
	frozen, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "docker",
		BuildProcess: &api.ComposeProcess{User: "1000:1000", WorkingDir: "/srv/app", StopSignal: "SIGINT", StopGracePeriodS: &grace}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                  string
		profile               []byte
		user, workdir, signal string
		grace                 int
	}{
		{"frozen overrides", frozen, "1000:1000", "/srv/app", "SIGINT", 20},
		{"no overrides keep the image's", nil, "", "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "source-process@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "source-process", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging, InferredProfile: tc.profile})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, sourceBuildArchive(t, nil), "builder/source", 1); err != nil {
				t.Fatal(err)
			}
			if dep, err = store.DeploymentByID(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			builder := &fakeBuilder{bytesOut: 128}
			h := New(store, &fakeNotifier{}, nil, builder, "", t.TempDir(), silentLogger())
			if err := h.buildLocalOCIAppLayer(ctx, app, &dep, acct, nil); err != nil {
				t.Fatal(err)
			}
			manifest := builder.calls[0].Manifest
			if tc.profile == nil {
				if manifest.User == "1000:1000" || manifest.WorkingDir == "/srv/app" || manifest.StopSignal == "SIGINT" {
					t.Fatalf("overrides applied without a frozen process: %+v", manifest)
				}
				return
			}
			if manifest.User != tc.user || manifest.WorkingDir != tc.workdir || manifest.StopSignal != tc.signal || manifest.StopGracePeriod != time.Duration(tc.grace)*time.Second {
				t.Fatalf("manifest user=%q workdir=%q signal=%q grace=%s; want %q %q %q %ds",
					manifest.User, manifest.WorkingDir, manifest.StopSignal, manifest.StopGracePeriod, tc.user, tc.workdir, tc.signal, tc.grace)
			}
		})
	}
}
