// adr: 20261025115958

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectDeletionPerKey pins ADR-20261025115958: a bucket holds one active
// deletion per key, only work on the same key fences a deletion or is fenced by
// one, and bucket-wide transitions still wait for every deletion.
func TestObjectDeletionPerKeyMem(t *testing.T) { objectDeletionPerKeySuite(t, state.NewMemStore()) }
func TestObjectDeletionPerKeyPG(t *testing.T) {
	st, _ := pgStore(t)
	objectDeletionPerKeySuite(t, st)
}

func objectDeletionPerKeySuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	d := st.(state.ObjectDeletionStore)
	begin := func(key string) (state.ObjectDeletion, error) {
		t.Helper()
		in := deletionCaptureInput(b)
		in.Key = key
		j, created, err := d.BeginObjectDeletion(ctx, in, accountingPolicy())
		if err == nil && !created {
			t.Fatal("new deletion replayed", key)
		}
		return j, err
	}
	if _, err := begin("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := begin("b"); err != nil {
		t.Fatal("deletion of another key waited", err)
	}
	if _, err := begin("a"); !errors.Is(err, state.ErrObjectBucketBusy) {
		t.Fatal("second deletion of one key was not busy", err)
	}

	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "c", 1, true, accountingPolicy()); err != nil {
		t.Fatal("write of an undeleted key was fenced", err)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "a", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("write of a key being deleted", err)
	}
	if err := st.(state.ObjectCapacityStore).BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "a", 1, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("tracked write of a key being deleted", err)
	}

	sessions := st.(state.ObjectMultipartUploadStore)
	upload := func(key string) error {
		u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: key, ExpiresAt: time.Now().Add(time.Hour)}, 100)
		if err == nil {
			_, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false)
		}
		return err
	}
	if err := upload("d"); err != nil {
		t.Fatal("multipart upload of an undeleted key was fenced", err)
	}
	if err := upload("b"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("multipart upload of a key being deleted", err)
	}
	// An unfinished multipart upload still conflicts with deleting its key.
	if _, err := begin("d"); !errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrObjectBucketBusy) {
		t.Fatal("deletion of a key with a multipart upload", err)
	}

	// Bucket-wide transitions wait for every deletion.
	if _, err := st.(state.ObjectBucketVersioningStore).RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("versioning passed the deletion fence", err)
	}
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "bucket-delete", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("bucket deletion passed the deletion fence", err)
	}
}

// TestObjectDeletionYieldsToInventory pins ADR-20261025115958: deletions wait
// while an inventory claim is fresh, so a stream of deletes cannot starve
// inventory, and a released claim lets them through at once.
func TestObjectDeletionYieldsToInventoryMem(t *testing.T) {
	objectDeletionYieldsToInventorySuite(t, state.NewMemStore())
}
func TestObjectDeletionYieldsToInventoryPG(t *testing.T) {
	st, _ := pgStore(t)
	objectDeletionYieldsToInventorySuite(t, st)
}

func objectDeletionYieldsToInventorySuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	d := st.(state.ObjectDeletionStore)
	active := deletionCaptureInput(b)
	active.Key = "active"
	if _, _, err := d.BeginObjectDeletion(ctx, active, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	// An active deletion does not block the claim; it yields to it.
	if err := st.ClaimObjectInventory(ctx, b.ID, "scan"); err != nil {
		t.Fatal("inventory claim beside an active deletion", err)
	}
	if _, _, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy()); !errors.Is(err, state.ErrObjectBucketBusy) {
		t.Fatal("deletion during an inventory claim was not busy", err)
	}
	releaser := st.(state.ObjectInventoryReleaser)
	if err := releaser.ReleaseObjectInventory(ctx, b.ID, "other"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("released another worker's claim", err)
	}
	if err := releaser.ReleaseObjectInventory(ctx, b.ID, "scan"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy()); err != nil {
		t.Fatal("deletion after the claim was released", err)
	}
	// The released attempt keeps its cadence.
	due, err := st.DueObjectInventories(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range due {
		if x.ID == b.ID {
			t.Fatal("a released attempt made the bucket due at once")
		}
	}
}
