package state_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 562
func TestObjectS3CopySourcesMem(t *testing.T) { objectS3CopySourceSuite(t, state.NewMemStore(), nil) }
func TestObjectS3CopySourcesPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	objectS3CopySourceSuite(t, st, pool)
}

func newCopySourceBucket(t *testing.T, st accountingStore, destination state.ObjectBucket, index int) state.ObjectBucket {
	t.Helper()
	b := destination
	b.ID, b.Name, b.PhysicalName = uuid.NewString(), fmt.Sprintf("source-%d", index), fmt.Sprintf("copy-source-%d", index)
	b.State = "provisioning"
	b, err := st.ReserveObjectBucket(t.Context(), b, 128)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(t.Context(), b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err = st.ClaimObjectInventory(t.Context(), b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(t.Context(), b.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	b.State = "ready"
	return b
}

func objectS3CopySourceSuite(t *testing.T, st accountingStore, pool *pgxpool.Pool) {
	b, _ := seedAccounting(t, st)
	sources := st.(state.ObjectS3CopySourceStore)
	credentials := st.(state.ObjectS3CredentialStore)
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	c, err := credentials.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "kid", Label: "copy writer", Permission: state.ObjectBucketPermissionWrite, Status: state.ObjectS3CredentialStatusActive}, 100)
	if err != nil {
		t.Fatal(err)
	}
	source := newCopySourceBucket(t, st, b, 0)
	g, err := sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "allowed/")
	if err != nil || g.Prefix != "allowed/" || g.SourceBucketID != source.ID {
		t.Fatal(g, err)
	}
	replay, err := sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "allowed/")
	if err != nil || replay.ID != g.ID {
		t.Fatal("idempotent update replaced grant", replay, err)
	}
	if _, _, err = sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, c.ID, source.ID, "other/key"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("prefix escape accepted", err)
	}
	for _, key := range []string{"", "allowed/\x00key", "allowed/\rkey", strings.Repeat("a", api.MaxObjectCopySourcePrefixBytes+1)} {
		if _, _, err = sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, c.ID, source.ID, key); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("invalid key accepted", err)
		}
	}
	got, resolved, err := sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, c.ID, source.ID, "allowed/key")
	if err != nil || got.ID != g.ID || resolved.ID != source.ID || resolved.PhysicalName != source.PhysicalName {
		t.Fatal(got, resolved, err)
	}
	if _, _, err = sources.ResolveObjectS3CopySource(t.Context(), uuid.NewString(), c.ID, source.ID, "allowed/key"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign account disclosed source", err)
	}
	if _, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, b.ID, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("same bucket granted", err)
	}
	for _, prefix := range []string{"x\x00", "x\n", strings.Repeat("x", api.MaxObjectCopySourcePrefixBytes+1)} {
		if _, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, prefix); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid prefix accepted", err)
		}
	}
	changed, err := sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "new/")
	if err != nil || changed.ID == g.ID || !changed.CreatedAt.Equal(g.CreatedAt) {
		t.Fatal("prefix changed without a new epoch", changed, err)
	}
	if _, _, err = sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, c.ID, source.ID, "allowed/key"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("stale prefix stayed live", err)
	}
	if err = sources.DeleteObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, c.ID, source.ID, "new/key"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked grant resolved", err)
	}
	g, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "new/")
	if err != nil || g.ID == changed.ID {
		t.Fatal("recreation reused stale epoch", g, err)
	}
	g = testCrossBucketCopyReceipts(t, st, pool, b, c, source, g)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, e := sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "new/")
			if e != nil {
				t.Error(e)
				return
			}
			ids <- g.ID
		}()
	}
	wg.Wait()
	close(ids)
	for id := range ids {
		if id != g.ID {
			t.Fatal("concurrent retry replaced authority", id)
		}
	}
	for i := 1; i <= api.MaxObjectS3CopySourcesPerCredential; i++ {
		src := newCopySourceBucket(t, st, b, i)
		_, e := sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, src.ID, "")
		if i < api.MaxObjectS3CopySourcesPerCredential && e != nil {
			t.Fatal(i, e)
		}
		if i == api.MaxObjectS3CopySourcesPerCredential && !errors.Is(e, state.ErrConflict) {
			t.Fatal("source limit was bypassed", i, e)
		}
		if i == api.MaxObjectS3CopySourcesPerCredential {
			var limit *state.ObjectStorageLimitError
			if !errors.As(e, &limit) || limit.Kind != "copy_sources_per_credential" || limit.Limit != api.MaxObjectS3CopySourcesPerCredential || limit.Observed != api.MaxObjectS3CopySourcesPerCredential+1 {
				t.Fatal("source limit lacks structured details", e)
			}
		}
	}
	list, err := sources.ListObjectS3CopySources(t.Context(), b.AccountID, b.ID, c.ID)
	if err != nil || len(list) != api.MaxObjectS3CopySourcesPerCredential {
		t.Fatal(list, err)
	}
	if pool != nil {
		if _, err = pool.Exec(t.Context(), `UPDATE object_s3_copy_source_grants SET prefix='forged/' WHERE id=$1`, g.ID); err == nil {
			t.Fatal("database accepted prefix without epoch")
		}
		_, err = pool.Exec(t.Context(), `UPDATE object_s3_copy_source_grants SET account_id=$1 WHERE id=$2`, uuid.NewString(), g.ID)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Fatal("database accepted foreign grant", err)
		}
		sources = state.NewPgStore(pool)
		if current, _, e := sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, c.ID, source.ID, "new/key"); e != nil || current.ID != g.ID {
			t.Fatal("restart lost authority", current, e)
		}
	}
	readonlyAccess, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	readonly, err := credentials.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: readonlyAccess, SecretSealed: []byte("sealed"), KID: "kid", Label: "reader", Permission: state.ObjectBucketPermissionRead, Status: state.ObjectS3CredentialStatusActive}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, readonly.ID, source.ID, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("read-only writer gained source authority", err)
	}
	if _, err = sources.SetObjectS3CopySource(t.Context(), uuid.NewString(), b.ID, c.ID, source.ID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign grant manager accepted", err)
	}
	other := b
	other.BackendID = "other-placement"
	other.BackendFingerprint = strings.Repeat("b", 64)
	mismatch := newCopySourceBucket(t, st, other, 2000)
	if _, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, mismatch.ID, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("cross-placement source accepted", err)
	}
	testCopySourceCredentialRotation(t, st, b, source)
	if err = credentials.RevokeObjectS3Credential(t.Context(), b.AccountID, b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, c.ID, source.ID, "new/key"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked writer retained source authority", err)
	}
	if err = sources.DeleteObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID); err != nil {
		t.Fatal("revoked credential prevented cleanup", err)
	}
}

func testCrossBucketCopyReceipts(t *testing.T, st accountingStore, pool *pgxpool.Pool, b state.ObjectBucket, c state.ObjectS3Credential, source state.ObjectBucket, g state.ObjectS3CopySource) state.ObjectS3CopySource {
	t.Helper()
	copies := st.(state.ObjectTrackedGatewayCopyStore)
	sources := st.(state.ObjectS3CopySourceStore)
	makeIntent := func() state.ObjectUploadCompletion {
		return state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: c.ID, Key: uuid.NewString(), Bytes: 1, Status: "pending", SourceKey: "new/key", SourceETag: `"source"`, SourceBucketID: source.ID, SourceCopyGrantID: g.ID}
	}
	fail := func(intent state.ObjectUploadCompletion) {
		t.Helper()
		intent.Status, intent.ErrorCode = "failed", "dispatch_failed"
		if _, err := copies.FinishTrackedObjectUpload(t.Context(), intent); err != nil {
			t.Fatal(err)
		}
	}
	wrong := makeIntent()
	wrong.SourceCopyGrantID = uuid.NewString()
	before, _ := st.ObjectUsage(t.Context(), b.AccountID, time.Now())
	if _, err := copies.BeginTrackedGatewayCopy(t.Context(), wrong, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("forged grant admitted", err)
	}
	after, _ := st.ObjectUsage(t.Context(), b.AccountID, time.Now())
	if after.Authorizations != before.Authorizations {
		t.Fatal("rejected grant spent quota")
	}
	prepared, err := copies.BeginTrackedGatewayCopy(t.Context(), makeIntent(), accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if pool != nil {
		if _, err = pool.Exec(t.Context(), `UPDATE object_upload_completions SET source_copy_grant_id=$1 WHERE id=$2`, uuid.NewString(), prepared.ID); err == nil {
			t.Fatal("database changed receipt provenance")
		}
	}
	forged := prepared
	forged.SourceCopyGrantID = uuid.NewString()
	forged.Status, forged.ErrorCode = "failed", "dispatch_failed"
	if _, err = copies.FinishTrackedObjectUpload(t.Context(), forged); !errors.Is(err, state.ErrConflict) {
		t.Fatal("forged finish accepted", err)
	}
	forged = prepared
	forged.SubjectID = uuid.NewString()
	forged.Status, forged.ErrorCode = "failed", "dispatch_failed"
	if _, err = copies.FinishTrackedObjectUpload(t.Context(), forged); !errors.Is(err, state.ErrConflict) {
		t.Fatal("receipt subject changed", err)
	}
	// Recreating the same grant cannot revive prepared work from the old epoch.
	if err = sources.DeleteObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	g, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "new/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = copies.DispatchTrackedObjectUpload(t.Context(), b.AccountID, b.ID, prepared.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale epoch dispatched", err)
	}
	fail(prepared)
	prepared, err = copies.BeginTrackedGatewayCopy(t.Context(), makeIntent(), accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if pool != nil {
		copies = state.NewPgStore(pool)
	}
	dispatched, err := copies.DispatchTrackedObjectUpload(t.Context(), b.AccountID, b.ID, prepared.ID)
	if err != nil || dispatched.SourceCopyGrantID != g.ID {
		t.Fatal(dispatched, err)
	}
	if err = sources.DeleteObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	dispatched.Status, dispatched.ETag = "completed", `"destination"`
	if _, err = copies.FinishTrackedObjectUpload(t.Context(), dispatched); err != nil {
		t.Fatal("revocation blocked already dispatched settlement", err)
	}
	// Each race either prevents dispatch or retains completion authority.
	for range 8 {
		g, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "new/")
		if err != nil {
			t.Fatal(err)
		}
		intent, e := copies.BeginTrackedGatewayCopy(t.Context(), makeIntent(), accountingPolicy())
		if e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		var dispatchErr, deleteErr error
		var out state.ObjectUploadCompletion
		go func() {
			defer wg.Done()
			out, dispatchErr = copies.DispatchTrackedObjectUpload(t.Context(), b.AccountID, b.ID, intent.ID)
		}()
		go func() {
			defer wg.Done()
			deleteErr = sources.DeleteObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID)
		}()
		wg.Wait()
		if deleteErr != nil {
			t.Fatal(deleteErr)
		}
		if dispatchErr == nil {
			out.Status, out.ETag = "completed", `"copied"`
			if _, e = copies.FinishTrackedObjectUpload(t.Context(), out); e != nil {
				t.Fatal(e)
			}
		} else if errors.Is(dispatchErr, state.ErrConflict) {
			fail(intent)
		} else {
			t.Fatal(dispatchErr)
		}
	}
	g, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "new/")
	if err != nil {
		t.Fatal(err)
	}
	sessions := st.(state.ObjectMultipartUploadStore)
	u, err := sessions.ReserveObjectMultipartUpload(t.Context(), state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "multipart-copy", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.ClaimObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(t.Context(), u.ID, "init", "provider-"+u.ID); err != nil {
		t.Fatal(err)
	}
	parts := st.(state.ObjectCrossBucketMultipartStore)
	authority := state.ObjectMultipartCopySource{SubjectID: c.ID, BucketID: source.ID, GrantID: g.ID, Key: "new/key"}
	token := uuid.NewString()
	invalid := authority
	invalid.GrantID = uuid.NewString()
	if err = parts.BeginObjectCrossBucketMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, token, 1, 3, 100, accountingPolicy(), invalid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("forged part authority accepted", err)
	}
	if err = parts.BeginObjectCrossBucketMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, token, 1, 3, 100, accountingPolicy(), authority); err != nil {
		t.Fatal(err)
	}
	if pool != nil {
		if _, err = pool.Exec(t.Context(), `UPDATE object_storage_multipart_part_grants SET source_key='forged' WHERE upload_id=$1 AND part_number=1`, u.ID); err == nil {
			t.Fatal("database changed dispatched part provenance")
		}
	}
	if err = sources.DeleteObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = parts.BeginObjectCrossBucketMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, uuid.NewString(), 2, 3, 100, accountingPolicy(), authority); !errors.Is(err, state.ErrConflict) {
		t.Fatal("revoked source admitted part", err)
	}
	if err = parts.SettleObjectMultipartPart(t.Context(), b.AccountID, u.ID, 1, token); err != nil {
		t.Fatal("revocation blocked dispatched part settlement", err)
	}
	if err = parts.BeginObjectMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, uuid.NewString(), 1, 3, 100, accountingPolicy()); err != nil {
		t.Fatal("normal replacement retained old source grant", err)
	}
	g, err = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, c.ID, source.ID, "new/")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func testCopySourceCredentialRotation(t *testing.T, st accountingStore, b, source state.ObjectBucket) {
	t.Helper()
	base := st.(state.Store)
	bindings := st.(state.ObjectS3CredentialBindingStore)
	sources := st.(state.ObjectS3CopySourceStore)
	parentAccess, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := bindings.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: parentAccess, SecretSealed: []byte("old"), KID: "kid", Label: "managed copy", Permission: state.ObjectBucketPermissionWrite, Status: state.ObjectS3CredentialStatusActive, ManagedAppID: b.AppID, ManagedScope: "default", ManagedPrefix: "GREGALE_COPY"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	secrets := []state.AppSecret{}
	for _, name := range []string{"GREGALE_COPY_ACCESS_KEY_ID", "GREGALE_COPY_SECRET_ACCESS_KEY"} {
		secret := state.AppSecret{AccountID: b.AccountID, AppID: b.AppID, Scope: "default", Key: name, Ciphertext: []byte("sealed"), Kid: "kid", ValueHash: "hash", ManagedObjectStorageCredentialID: parent.ID}
		if err = base.PutManagedObjectStorageSecret(t.Context(), secret); err != nil {
			t.Fatal(err)
		}
		secrets = append(secrets, secret)
	}
	grant, err := sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, parent.ID, source.ID, "allowed/")
	if err != nil {
		t.Fatal(err)
	}
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := bindings.StageObjectS3CredentialRotation(t.Context(), state.ObjectS3CredentialRotationRequest{AccountID: b.AccountID, BucketID: b.ID, BindingID: parent.ID, WakeID: uuid.NewString(), AccessKeyID: access, SecretSealed: []byte("new"), KID: "kid", Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{parent.AccessKeyID, rotated.AccessKeyID} {
		current, _, e := bindings.ResolveObjectS3Credential(t.Context(), key)
		if e != nil {
			t.Fatal(e)
		}
		g, _, e := sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, current.ID, source.ID, "allowed/key")
		if e != nil || g.ID != grant.ID {
			t.Fatal("rotation lost parent grants", current, g, e)
		}
		if current.RotationParentID != "" {
			if _, e = sources.SetObjectS3CopySource(t.Context(), b.AccountID, b.ID, current.ID, source.ID, ""); !errors.Is(e, state.ErrNotFound) {
				t.Fatal("rotation stage acquired independent authority", e)
			}
		}
	}
	if err = sources.DeleteObjectS3CopySource(t.Context(), b.AccountID, b.ID, parent.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{parent.AccessKeyID, rotated.AccessKeyID} {
		current, _, e := bindings.ResolveObjectS3Credential(t.Context(), key)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = sources.ResolveObjectS3CopySource(t.Context(), b.AccountID, current.ID, source.ID, "allowed/key"); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("rotation retained revoked grant", e)
		}
	}
}
