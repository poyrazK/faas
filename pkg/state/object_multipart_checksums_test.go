package state_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1038
func TestObjectMultipartChecksumsMem(t *testing.T) { multipartChecksums(t, state.NewMemStore()) }
func TestObjectMultipartChecksumsPG(t *testing.T)  { st, _ := pgStore(t); multipartChecksums(t, st) }

func prepareChecksumResult(t *testing.T, st accountingStore) state.ObjectMultipartUpload {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	sessions := st.(state.ObjectMultipartUploadStore)
	p := objectchecksum.Policy{Algorithm: "CRC32C", Type: objectchecksum.FullObject}
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "checksum-object", ExpiresAt: time.Now().Add(time.Hour), Checksum: objectchecksum.Snapshot{Digest: objectchecksum.Digest{Policy: p}}}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(ctx, u.AccountID, u.AppID, u.BucketID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(ctx, u.ID, "init", "private-checksum-upload"); err != nil {
		t.Fatal(err)
	}
	x := st.(state.ObjectMultipartTransferStore)
	if err = x.BeginObjectMultipartPart(ctx, u.AccountID, u.BucketID, u.ID, "part", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if err = x.SettleObjectMultipartPart(ctx, u.AccountID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(ctx, u.AccountID, u.AppID, u.BucketID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := objectchecksum.NewHash(p.Algorithm)
	_, _ = h.Write([]byte(strings.Repeat("a", 30)))
	u.Checksum, err = objectchecksum.Build(p, []objectchecksum.Part{{Number: 1, Size: 30, Value: base64.StdEncoding.EncodeToString(h.Sum(nil))}})
	if err != nil {
		t.Fatal(err)
	}
	u, err = x.PrepareObjectMultipartCompletion(ctx, u, "complete", 30, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err = st.(state.ObjectMultipartCompletionStore).DispatchObjectMultipartCompletion(ctx, u); err != nil {
		t.Fatal(err)
	}
	return u
}

func multipartChecksums(t *testing.T, st accountingStore) {
	t.Helper()
	u := prepareChecksumResult(t, st)
	results := st.(state.ObjectMultipartCompletionStore)
	for _, digest := range []objectchecksum.Digest{{}, {Policy: u.Checksum.Policy, Value: "AAAAAA=="}} {
		if _, err := results.FinishObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{ETag: `"actual"`, Checksum: digest}); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unverified checksum settled", err)
		}
	}
	forged := u
	forged.Checksum = u.Checksum.Clone()
	forged.Checksum.Parts[0].Size++
	if err := results.DispatchObjectMultipartCompletion(t.Context(), forged); !errors.Is(err, state.ErrConflict) {
		t.Fatal("checksum intent changed", err)
	}
	if err := st.(state.ObjectMultipartUploadStore).FinishObjectMultipartUpload(t.Context(), u.ID, u.LeaseToken, state.ObjectMultipartCompleted); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old completion bypassed checksum verification", err)
	}
	out, err := results.FinishObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{ETag: `"actual"`, Checksum: u.Checksum.Digest})
	if err != nil || !out.Checksum.Equal(u.Checksum) || out.State != state.ObjectMultipartCompleted {
		t.Fatal(out, err)
	}
	stored, err := st.(state.ObjectMultipartUploadStore).GetObjectMultipartUpload(t.Context(), u.AccountID, u.AppID, u.BucketID, u.ID)
	if err != nil || !stored.Checksum.Equal(u.Checksum) {
		t.Fatal("checksum intent lost after settlement", stored, err)
	}
	out.Checksum.Parts[0].Value = "AAAAAA=="
	again, _ := st.(state.ObjectMultipartUploadStore).GetObjectMultipartUpload(t.Context(), u.AccountID, u.AppID, u.BucketID, u.ID)
	if !again.Checksum.Equal(u.Checksum) {
		t.Fatal("snapshot escaped store ownership")
	}
}

func TestObjectMultipartChecksumOldWriterAndRollbackPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	u := prepareChecksumResult(t, st)
	for _, sql := range []string{`UPDATE object_storage_multipart_uploads SET checksum_snapshot='{}' WHERE id=$1`, `UPDATE object_storage_multipart_uploads SET state='completed',completion_etag='actual' WHERE id=$1`} {
		if _, err := pool.Exec(t.Context(), sql, u.ID); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatal("old writer bypassed checksum journal", err)
		}
	}
	file, err := migrations.FS.ReadFile("20261013135901265_object_multipart_checksums.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(file), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	if _, err = pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "retained multipart checksum intent") {
		t.Fatal("rollback lost accepted checksum intent", err)
	}
	row, err := state.NewPgStore(pool).GetObjectMultipartUpload(t.Context(), u.AccountID, u.AppID, u.BucketID, u.ID)
	if err != nil || !row.Checksum.Equal(u.Checksum) {
		t.Fatal("reconstructed owner lost checksum snapshot", row, err)
	}
}
