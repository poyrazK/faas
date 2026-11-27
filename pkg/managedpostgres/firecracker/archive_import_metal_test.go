//go:build metal && linux

package firecracker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func TestMetalRestorePortableVectorArchive(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_VECTOR_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_VECTOR_BASE to a PostgreSQL 16 base with managed pgvector")
	}
	stack := newMetalStackWith(t, workdir, map[int]string{vectorSourceMajor: base})
	p, connect := stack.p, connectThroughProxy(stack.proxyAddr)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	source, target := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_ = stack.sched.Delete(context.Background(), target)
		_ = stack.sched.Delete(context.Background(), source)
	})
	spec := devSpec()
	spec.PostgresMajor = vectorSourceMajor
	for _, id := range []string{source, target} {
		provisionReady(ctx, t, p, managedpostgres.ProvisionRequest{ResourceID: id, Spec: spec})
	}
	catalog, _, err := p.Extensions(ctx, source, pgextension.VectorName, 0)
	if err != nil || !slices.ContainsFunc(catalog.Installed, func(e pgextension.Installed) bool { return e.Name == pgextension.VectorName }) {
		t.Fatalf("enable vector: %+v, %v", catalog, err)
	}
	migrate := issueVectorCredential(ctx, t, p, source, "archive-source-migration", managedpostgres.CredentialMigration)
	metalQuery(ctx, t, connect, migrate, vectorSeedSQL)
	var dump bytes.Buffer
	info, err := p.ExportDatabase(ctx, source, spec.PostgresMajor, &dump)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(dump.Bytes())
	manifest := pgarchive.Manifest{Source: info, SizeBytes: int64(dump.Len()), SHA256: hex.EncodeToString(hash[:])}
	targetAdmin := issueVectorCredential(ctx, t, p, target, "archive-target-migration", managedpostgres.CredentialMigration)
	metalQuery(ctx, t, connect, targetAdmin, "CREATE FUNCTION public.block_archive() RETURNS int LANGUAGE SQL AS 'SELECT 1'")
	status, err := p.ImportArchive(ctx, target, uuid.NewString(), manifest, bytes.NewReader(dump.Bytes()))
	if err != nil || status.State != managedpostgres.ImportFailed || status.ErrorCode != "target_not_empty" {
		t.Fatalf("preflight for a nonempty target: %+v, %v", status, err)
	}
	metalQuery(ctx, t, connect, targetAdmin, "DROP FUNCTION public.block_archive()")
	corrupt := bytes.Clone(dump.Bytes())
	corrupt[len(corrupt)-1] ^= 1
	if _, err := p.ImportArchive(ctx, target, uuid.NewString(), manifest, bytes.NewReader(corrupt)); !errors.Is(err, pgarchive.ErrArchive) {
		t.Fatalf("corrupt upload: %v", err)
	}
	await := func(operation string) managedpostgres.ImportStatus {
		t.Helper()
		for {
			progress, err := p.ImportStatus(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			if progress.State != managedpostgres.ImportRunning {
				if operation != "" && progress.OperationID != operation {
					t.Fatalf("operation changed: %+v", progress)
				}
				return progress
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(150 * time.Millisecond):
			}
		}
	}
	if got := await(""); got.State != managedpostgres.ImportFailed {
		t.Fatalf("corrupt upload status: %+v", got)
	}
	if got := metalQuery(ctx, t, connect, targetAdmin, "SELECT (to_regclass('public.documents') IS NULL)::text"); got != "true" {
		t.Fatal("corrupt upload wrote data to the target")
	}

	operation := uuid.NewString()
	status, err = p.ImportArchive(ctx, target, operation, manifest, bytes.NewReader(dump.Bytes()))
	if err != nil || (status.State != managedpostgres.ImportRunning && status.State != managedpostgres.ImportDone) {
		t.Fatalf("start restore: %+v, %v", status, err)
	}
	if got := await(operation); got.State != managedpostgres.ImportDone {
		t.Fatalf("restore status: %+v", got)
	}
	app := issueVectorCredential(ctx, t, p, target, "archive-restored-app", managedpostgres.CredentialReadWrite)
	expectVectorNeighbor(ctx, t, connect, app, spec.PostgresMajor, "original")
}
