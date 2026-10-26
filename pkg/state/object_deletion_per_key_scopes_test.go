// adr: 20261025115958

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectCopyDeletionPerKey pins the ADR-20261025115958 amendment: a
// cross-bucket copy waits only for a deletion of its source key. Its
// destination is an ordinary write, fenced per key at admission.
func TestObjectCopyDeletionPerKeyMem(t *testing.T) { objectCopyDeletionPerKeySuite(t, state.NewMemStore()) }
func TestObjectCopyDeletionPerKeyPG(t *testing.T) {
	st, _ := pgStore(t)
	objectCopyDeletionPerKeySuite(t, st)
}

func objectCopyDeletionPerKeySuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.(state.ObjectS3CredentialStore).CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "kid", Label: "copy writer", Permission: state.ObjectBucketPermissionWrite, Status: state.ObjectS3CredentialStatusActive}, 100)
	if err != nil {
		t.Fatal(err)
	}
	source := newCopySourceBucket(t, st, b, 0)
	sources := st.(state.ObjectS3CopySourceStore)
	g, err := sources.SetObjectS3CopySource(ctx, b.AccountID, b.ID, c.ID, source.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	d := st.(state.ObjectDeletionStore)
	deleteKey := func(bucket state.ObjectBucket, key string) {
		t.Helper()
		in := deletionCaptureInput(bucket)
		in.Key = key
		if _, _, err := d.BeginObjectDeletion(ctx, in, accountingPolicy()); err != nil {
			t.Fatal(err)
		}
	}
	deleteKey(source, "deleting")
	deleteKey(b, "destination-deleting")

	if _, _, err = sources.ResolveObjectS3CopySource(ctx, b.AccountID, c.ID, source.ID, "kept"); err != nil {
		t.Fatal("deletions of other keys fenced the copy", err)
	}
	if _, _, err = sources.ResolveObjectS3CopySource(ctx, b.AccountID, c.ID, source.ID, "deleting"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("copy of a source key being deleted resolved", err)
	}

	copies := st.(state.ObjectTrackedGatewayCopyStore)
	begin := func(sourceKey, key string) error {
		_, err := copies.BeginTrackedGatewayCopy(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: c.ID, Key: key, Bytes: 1, Status: "pending", SourceKey: sourceKey, SourceETag: `"source"`, SourceBucketID: source.ID, SourceCopyGrantID: g.ID}, accountingPolicy())
		return err
	}
	if err = begin("kept", "copied"); err != nil {
		t.Fatal("copy receipt waited for another key's deletion", err)
	}
	if err = begin("deleting", "copied-2"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("copy receipt of a source key being deleted", err)
	}
	if err = begin("kept", "destination-deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("copy onto a destination key being deleted", err)
	}
}

// TestObjectLifecycleDeletionScope pins the ADR-20261025115958 amendment: a
// lifecycle scan pauses for its own unsettled deletions, not for a customer's
// deletion of another key.
func TestObjectLifecycleDeletionScopeMem(t *testing.T) {
	objectLifecycleDeletionScopeSuite(t, state.NewMemStore())
}
func TestObjectLifecycleDeletionScopePG(t *testing.T) {
	st, _ := pgStore(t)
	objectLifecycleDeletionScopeSuite(t, st)
}

func objectLifecycleDeletionScopeSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	l := st.(state.ObjectLifecycleStore)
	d := st.(state.ObjectDeletionStore)
	scoped := st.(state.ObjectLifecycleDeletionActivityStore)
	days := int32(1)
	rules := []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "logs/"}, Expiration: &api.ObjectLifecycleExpiration{Days: &days}}}
	if _, err := l.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, rules); err != nil {
		t.Fatal(err)
	}
	scan, err := l.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scan, err = l.ClaimObjectLifecycleScan(ctx, scan.ID, "scan-owner"); err != nil {
		t.Fatal(err)
	}
	paused := func() bool {
		t.Helper()
		active, err := scoped.HasActiveObjectLifecycleDeletion(ctx, b.ID, scan.ID)
		if err != nil {
			t.Fatal(err)
		}
		return active
	}
	customer := deletionCaptureInput(b)
	customer.Key = "logs/customer"
	if _, _, err = d.BeginObjectDeletion(ctx, customer, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if paused() {
		t.Fatal("a customer deletion of another key paused the scan")
	}
	if active, err := st.(state.ObjectDeletionActivityStore).HasActiveObjectDeletion(ctx, b.AccountID, b.AppID, b.ID); err != nil || !active {
		t.Fatal("bucket-wide activity hid the customer deletion", active, err)
	}
	modified := time.Now().UTC().AddDate(0, 0, -5).Truncate(time.Second)
	own := state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "logs/a"}, AccountID: b.AccountID, AppID: b.AppID, Token: "delete-owner", Lifecycle: &state.ObjectLifecycleDeletionBinding{ScanID: scan.ID, ScanToken: scan.Token, RuleID: "expire", Kind: "current", ExpectedProviderVersionID: "null", ExpectedLastModified: modified}}
	if _, _, err = d.BeginObjectDeletion(ctx, own, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if !paused() {
		t.Fatal("the scan's own unsettled deletion did not pause it")
	}
	if active, err := scoped.HasActiveObjectLifecycleDeletion(ctx, b.ID, uuid.NewString()); err != nil || active {
		t.Fatal("another scan saw this scan's deletion", active, err)
	}
}
