//go:build metal && linux

// adr: 568 — original disk receipts are verified separately from VM restore.
package fcvm

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func nativeMetalPublicationReceiptBytes(t *testing.T, ctx context.Context, v *JailerVMM, incoming nativeQualificationRecord) int64 {
	t.Helper()
	j := v.nativeRecovery.publications.(*linuxNativeSnapshotPublicationJournal)
	intent, err := j.readLocked(ctx, incoming.Generation)
	if err != nil {
		t.Fatal(err)
	}
	var stored int64
	for _, kind := range []string{"mem", "vmstate", "drive", "backing"} {
		r, err := j.readObjectLocked(ctx, intent, kind)
		if err != nil {
			t.Fatal("original receipt missing:", kind, err)
		}
		reader, err := storage.GetExclusiveArtifact(ctx, v.storage, r.Object)
		if err != nil {
			t.Fatal("original receipt cannot open its object:", kind, err)
		}
		read, err := io.Copy(io.Discard, reader)
		if err := errors.Join(err, reader.Close()); err != nil || read != r.Object.LogicalBytes {
			t.Fatal("original artifact failed receipt verification:", kind, read, err)
		}
		if kind != "backing" {
			stored += r.Object.StoredBytes
		}
		t.Logf("original %s receipt: logical=%d allocated=%d inode=%d", kind, r.Object.LogicalBytes, r.Object.StoredBytes, r.Object.Local.Inode)
	}
	return stored
}
