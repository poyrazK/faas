// adr: 20261103112845
package apidsource

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A Compose service built from source listens on its ports: target, so its
// deployment carries that port; without one the build decides, as before.
func TestEnqueueSourceBuildCarriesComposePort(t *testing.T) {
	for _, tc := range []struct {
		name string
		port int
	}{{"compose port", 3000}, {"no compose port", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			st := state.NewMemStore()
			app := mustSeedApp(t, st)
			srcPath, srcBytes := stageSource(t, t.TempDir())
			res, err := Enqueue(context.Background(), st, &recordingNotifier{}, EnqueueParams{
				AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: srcPath, SourceBytes: srcBytes,
				Source: "tarball", LogSpool: t.TempDir(), Log: quietLogger(), ImagePort: tc.port,
			})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := st.DeploymentByID(context.Background(), res.DeploymentID)
			if err != nil {
				t.Fatal(err)
			}
			if dep.OverridePort != tc.port {
				t.Fatalf("override port = %d, want %d", dep.OverridePort, tc.port)
			}
		})
	}
}

func TestEnqueueSourceBuildRefusesInvalidPort(t *testing.T) {
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	srcPath, srcBytes := stageSource(t, t.TempDir())
	if _, err := Enqueue(context.Background(), st, &recordingNotifier{}, EnqueueParams{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: srcPath, SourceBytes: srcBytes,
		Source: "tarball", LogSpool: t.TempDir(), Log: quietLogger(), ImagePort: 70000,
	}); err == nil {
		t.Fatal("port 70000 accepted")
	}
}
