package imaged

import (
	"errors"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceSignatureRenewalWithoutExportArchive(t *testing.T) {
	for _, function := range []bool{false, true} {
		t.Run(map[bool]string{false: "app", true: "function"}[function], func(t *testing.T) {
			f := newSourceExportFixture(t, state.DeploymentKindTarball, function, true)
			if err := f.h.consumeSourceBuild(t.Context(), f.app, f.dep, f.acct); err != nil {
				t.Fatal(err)
			}
			before, err := f.store.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(f.dep.RootfsPath); err != nil {
				t.Fatal(err)
			}
			if present, err := f.h.renewProducedDeploymentSignatures(t.Context(), f.app, f.dep); err != nil || !present {
				t.Fatal("retained source proof needs removed archive", err)
			}
			after, err := f.store.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.app.AccountID, f.app.ID, f.dep.ID)
			if err != nil || after.InputHash != before.InputHash || !after.ExpiresAt.After(before.ExpiresAt) {
				t.Fatal("source renewal lost stable producer binding", err)
			}
			if err := f.store.DeleteAppTrustedSigner(t.Context(), f.app.AccountID, f.app.ID, "company"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.renewProducedDeploymentSignatures(t.Context(), f.app, f.dep); !errors.Is(err, buildpublisher.ErrInvalid) {
				t.Fatal("source renewal bypassed publisher revocation", err)
			}
		})
	}
}
