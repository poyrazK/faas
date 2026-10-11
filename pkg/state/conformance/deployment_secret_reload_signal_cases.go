package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testDeploymentSecretReloadSignalSurvivesRead reproduces the production-us
// rc.242 outage: PgStore's deployment scan never read secret_reload_signal,
// so every Postgres-loaded deployment reported SecretReloadSignalKnown=false
// while the runtime-value projection (secret_reload_signal IS NOT NULL)
// reported true. schedd's runtime-input check then rejected every wake and
// prime. A persisted signal, including the explicit empty opt-out, must be
// known on every read.
func testDeploymentSecretReloadSignalSurvivesRead(t *testing.T, fx *Fixture) {
	t.Helper()
	for _, signal := range []string{"SIGHUP", ""} {
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: fx.App.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:reload-signal-" + signal, Status: state.DeployPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.Store.SetDeploymentSecretReloadSignal(fx.Ctx, dep.ID, signal); err != nil {
			t.Fatal(err)
		}
		stored, err := fx.Store.DeploymentByID(fx.Ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.SecretReloadSignal != signal || !stored.SecretReloadSignalKnown {
			t.Fatalf("signal %q read back as (%q, known=%v), want (%q, known=true)",
				signal, stored.SecretReloadSignal, stored.SecretReloadSignalKnown, signal)
		}
	}
}
