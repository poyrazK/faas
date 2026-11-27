//go:build metal && linux

package firecracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func TestMetalManagedVectorUpgradeAndRestore(t *testing.T) {
	workdir, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_GUEST_INIT to run native pgvector upgrades")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	digest, err := dbvm.FileDigest(guestInit)
	if err != nil {
		t.Fatal(err)
	}
	bases := make(map[int]string)
	for _, major := range []int{vectorSourceMajor, 18, 17} {
		image := dbvm.PostgresImages[major]
		path := dbvm.BasePath(filepath.Join(workdir, "major-bases"), major, image, digest)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := dbvm.BuildBase(ctx, image, guestInit, path); err != nil {
				t.Fatalf("build the PostgreSQL %d base: %v", major, err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		bases[major] = path
	}

	for _, targetMajor := range []int{18, 17} {
		t.Run(fmt.Sprintf("16-to-%d", targetMajor), func(t *testing.T) {
			stack := newMetalStackWith(t, workdir, bases)
			p, connect := stack.p, connectThroughProxy(stack.proxyAddr)
			source, beforeCopy, afterCopy := uuid.NewString(), uuid.NewString(), uuid.NewString()
			t.Cleanup(func() {
				for _, id := range []string{afterCopy, beforeCopy, source} {
					_ = stack.sched.Delete(context.Background(), id)
				}
			})
			beforeSpec := devSpec()
			beforeSpec.PostgresMajor, beforeSpec.RestoreWindowSeconds = vectorSourceMajor, 3600
			afterSpec := beforeSpec
			afterSpec.PostgresMajor = targetMajor
			provisionReady(ctx, t, p, managedpostgres.ProvisionRequest{ResourceID: source, Spec: beforeSpec})
			wantVector := pgextension.Installed{Name: pgextension.VectorName, Version: pgextension.VectorVersion}
			catalog, _, err := p.Extensions(ctx, source, pgextension.VectorName, 0)
			if err != nil || !slices.Contains(catalog.Installed, wantVector) {
				t.Fatalf("enable vector: %+v, %v", catalog, err)
			}
			migration := issueVectorCredential(ctx, t, p, source, "vector-upgrade-migration", managedpostgres.CredentialMigration)
			metalQuery(ctx, t, connect, migration, vectorSeedSQL)
			app := issueVectorCredential(ctx, t, p, source, "vector-upgrade-app", managedpostgres.CredentialReadWrite)
			expectVectorNeighbor(ctx, t, connect, app, beforeSpec.PostgresMajor, "original")
			before := time.Now().UTC().Truncate(time.Microsecond)
			time.Sleep(1100 * time.Millisecond)
			metalQuery(ctx, t, connect, app, "INSERT INTO documents VALUES ('before-upgrade', '[0.99,0,0]')")

			if _, blockers, err := p.Extensions(ctx, source, "", targetMajor); err != nil || len(blockers) != 0 {
				t.Fatalf("PostgreSQL %d upgrade preflight: blockers %v, %v", targetMajor, blockers, err)
			}
			upgrade := managedpostgres.UpdateRequest{ResourceID: source, DataResourceID: source, PreviousSpec: beforeSpec, Spec: afterSpec, IdempotencyKey: uuid.NewString()}
			waitForVectorDatabase(ctx, t, targetMajor, func() (managedpostgres.ObservedDatabase, error) { return p.Update(ctx, upgrade) })
			expectVectorNeighbor(ctx, t, connect, app, targetMajor, "before-upgrade")
			catalog, _, err = p.Extensions(ctx, source, "", 0)
			wantAvailable := pgextension.Available{Name: pgextension.VectorName, Version: pgextension.VectorVersion, Default: true}
			if err != nil || !slices.Contains(catalog.Installed, wantVector) || !slices.Contains(catalog.Available, wantAvailable) {
				t.Fatalf("vector catalog after upgrade: %+v, %v", catalog, err)
			}
			after := time.Now().UTC().Truncate(time.Microsecond)
			time.Sleep(1100 * time.Millisecond)
			metalQuery(ctx, t, connect, app, "INSERT INTO documents VALUES ('after-upgrade', '[1,0,0]')")
			expectVectorNeighbor(ctx, t, connect, app, targetMajor, "after-upgrade")

			for _, point := range []struct {
				id   string
				spec managedpostgres.Spec
				at   time.Time
				want string
			}{
				{id: beforeCopy, spec: beforeSpec, at: before, want: "original"},
				{id: afterCopy, spec: afterSpec, at: after, want: "before-upgrade"},
			} {
				restore := managedpostgres.RestoreRequest{ResourceID: point.id, SourceResourceID: source, Spec: point.spec, PointInTime: point.at, IdempotencyKey: point.id}
				waitForVectorDatabase(ctx, t, point.spec.PostgresMajor, func() (managedpostgres.ObservedDatabase, error) { return p.Restore(ctx, restore) })
				copyApp := issueVectorCredential(ctx, t, p, point.id, "vector-restored-app", managedpostgres.CredentialReadWrite)
				expectVectorNeighbor(ctx, t, connect, copyApp, point.spec.PostgresMajor, point.want)
			}
		})
	}
}
