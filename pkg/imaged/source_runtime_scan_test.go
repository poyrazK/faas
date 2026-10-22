package imaged

// adr: 435. Injected composition and scanner prove plumbing, not native bytes.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceComposedRuntimeScanPublication(t *testing.T) {
	for _, function := range []bool{false, true} {
		t.Run(map[bool]string{false: "app", true: "function"}[function], func(t *testing.T) {
			f := newSourceExportFixture(t, state.DeploymentKindTarball, function, true)
			if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err != nil {
				t.Fatal(err)
			}
			inputs, err := f.store.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "package"), []byte("source guest packages"), 0600); err != nil {
				t.Fatal(err)
			}
			owner := &fixtureRuntimeScanOwner{source: source}
			result, err := scanProducedRuntime(t.Context(), f.store, owner, func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil }, inputs, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(owner.target); !os.IsNotExist(err) {
				t.Fatal("projection retained before publication", err)
			}
			value, err := publishProducedRuntimeScan(t.Context(), f.store, result)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := f.store.GetFreshDeploymentRuntimeScan(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID)
			if err != nil || fresh.Scan.ID != value.ID || len(value.Input.Facts.Views) != 1 || value.Input.Artifacts[1].Kind != inputs.Artifacts[1].Kind {
				t.Fatal("source scan lost distinct producer binding", err)
			}
			if _, err := f.store.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
				t.Fatal("source scan minted native authority", err)
			}
			if err := f.store.DeleteAppTrustedSigner(t.Context(), f.app.AccountID, f.app.ID, "company"); err != nil {
				t.Fatal(err)
			}
			if _, err := publishProducedRuntimeScan(t.Context(), f.store, result); !errors.Is(err, buildpublisher.ErrInvalid) {
				t.Fatal("late revocation published source facts", err)
			}
		})
	}
}
