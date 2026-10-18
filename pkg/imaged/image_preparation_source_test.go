package imaged

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceImagePreparationDefersEvidenceUntilAssemblyCompletes(t *testing.T) {
	for _, function := range []bool{false, true} {
		t.Run(map[bool]string{false: "app", true: "function"}[function], func(t *testing.T) {
			f := newSourceExportFixture(t, state.DeploymentKindTarball, function, true)
			p, err := f.store.BeginImagePreparation(t.Context(), f.dep.ID, "source-node")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), imagePreparationClaimKey{}, imagePreparationClaim{id: f.dep.ID, token: p.ClaimToken, store: f.store})
			f.builder.buildHook = func() {
				dep, err := f.store.DeploymentByID(ctx, f.dep.ID)
				if err != nil || dep.RootfsPath != f.dep.RootfsPath {
					t.Fatal("unfinished assembly published rootfs", err)
				}
				if _, err := f.store.GetCurrentSourceBuildRootfs(ctx, f.app.AccountID, f.app.ID, f.dep.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("unfinished assembly selected producer", err)
				}
			}
			if err := f.h.prepareImageLayer(ctx, f.store, f.app, f.dep, p); err != nil {
				t.Fatal(err)
			}
			value, err := f.store.GetCurrentSourceBuildRootfs(ctx, f.app.AccountID, f.app.ID, f.dep.ID)
			if err != nil {
				t.Fatal("completed assembly lost signed source binding", err)
			}
			resumed, err := f.store.BeginImagePreparation(ctx, f.dep.ID, "source-node")
			if err != nil || resumed.Phase != state.ImageLayerPublished || resumed.InputPath != f.dep.RootfsPath || value.Input.PublicationID == "" {
				t.Fatal("source checkpoint cannot resume", err)
			}
		})
	}
}
