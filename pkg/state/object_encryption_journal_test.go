package state_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func journalEncryption(account string) state.ObjectEncryptionSnapshot {
	account = uuid.MustParse(account).String()
	disabled := false
	return state.ObjectEncryptionSnapshot{AccountID: account, Selection: api.ObjectEncryption{Algorithm: "aws:kms", KeyID: "arn:gregale:kms:us-east-1:" + account + ":key/" + uuid.NewString(), BucketKeyEnabled: &disabled, Context: base64.StdEncoding.EncodeToString([]byte(`{"purpose":"journal"}`))}, ProviderKeyID: "provider-private-key", KeyIdentity: strings.Repeat("a", 64)}
}

// adr: 555
func TestObjectEncryptionJournalMem(t *testing.T) { encryptionJournalSuite(t, state.NewMemStore()) }
func TestObjectEncryptionJournalPG(t *testing.T)  { st, _ := pgStore(t); encryptionJournalSuite(t, st) }

func encryptionJournalSuite(t *testing.T, st accountingStore) {
	b, _ := seedAccounting(t, st)
	if _, _, err := st.(state.EventSubscriptionStore).UpsertEventSubscription(t.Context(), b.AccountID, b.AppID, api.ObjectEventSource, "object.*", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	writes := st.(state.ObjectTrackedGatewayUploadStore)
	e := journalEncryption(b.AccountID)
	original := e.Clone()
	c, err := writes.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "encrypted", Bytes: 10, Status: "pending", Encryption: e}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	// Admission and returned values cannot retain caller-owned pointers.
	*e.Selection.BucketKeyEnabled = true
	*c.Encryption.Selection.BucketKeyEnabled = true
	c, err = writes.GetObjectUploadReceipt(t.Context(), b.AccountID, b.AppID, "", "subject", c.ID)
	if err != nil || !c.Encryption.Equal(original) {
		t.Fatal("snapshot aliased", c, err)
	}
	if _, err = writes.GetObjectUploadReceipt(t.Context(), uuid.NewString(), b.AppID, "", "subject", c.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-owner receipt", err)
	}
	c, err = writes.DispatchTrackedObjectUpload(t.Context(), b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag = "completed", `"actual"`
	for _, bad := range []state.ObjectUploadCompletion{c, cloneJournalWithDifferentIdentity(c), cloneJournalWithDifferentSelection(c)} {
		if _, err = writes.FinishTrackedObjectUpload(t.Context(), bad); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unverified or changed encryption settled", err)
		}
	}
	noObjectEvent(t, st)
	receipt, err := st.(state.ObjectWriteReceiptStore).GetObjectWriteReceipt(t.Context(), b.AccountID, b.AppID, b.ID, c.ID)
	if err != nil || receipt.Status != "pending" {
		t.Fatal("failed verification released intent", receipt, err)
	}
	raw, err := json.Marshal(c)
	if err != nil || strings.Contains(string(raw), "provider-private") || strings.Contains(string(raw), "bucket_key_enabled") {
		t.Fatal("private snapshot leaked", string(raw), err)
	}
	c.VerifiedEncryption = original.Selection
	done, err := writes.FinishTrackedObjectUpload(t.Context(), c)
	if err != nil || done.Status != "completed" || !done.Encryption.Equal(original) {
		t.Fatal(done, err)
	}
	// A late callback cannot replace a stored proof after settlement.
	changed := cloneJournalWithDifferentIdentity(c)
	if _, err = writes.FinishTrackedObjectUpload(t.Context(), changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("terminal proof changed", err)
	}
	takeObjectEvent(t, st, b.AccountID, "write:"+c.ID, api.ObjectEventCreated)
	noObjectEvent(t, st)
}

func cloneJournalWithDifferentIdentity(c state.ObjectUploadCompletion) state.ObjectUploadCompletion {
	c.Encryption = c.Encryption.Clone()
	c.Encryption.KeyIdentity = strings.Repeat("b", 64)
	c.VerifiedEncryption = c.Encryption.Selection
	return c
}
func cloneJournalWithDifferentSelection(c state.ObjectUploadCompletion) state.ObjectUploadCompletion {
	c.Encryption = c.Encryption.Clone()
	*c.Encryption.Selection.BucketKeyEnabled = true
	c.VerifiedEncryption = c.Encryption.Selection
	return c
}

func TestObjectEncryptionMultipartJournalMem(t *testing.T) {
	encryptionMultipartJournalSuite(t, state.NewMemStore())
}
func TestObjectEncryptionMultipartJournalPG(t *testing.T) {
	st, _ := pgStore(t)
	encryptionMultipartJournalSuite(t, st)
}

func encryptionMultipartJournalSuite(t *testing.T, st accountingStore) {
	b, _ := seedAccounting(t, st)
	if _, _, err := st.(state.EventSubscriptionStore).UpsertEventSubscription(t.Context(), b.AccountID, b.AppID, api.ObjectEventSource, "object.*", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	results := st.(state.ObjectMultipartCompletionStore)
	e := journalEncryption(b.AccountID)
	original := e.Clone()
	u, err := sessions.ReserveObjectMultipartUpload(t.Context(), state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "multipart-encrypted", ExpiresAt: time.Now().Add(time.Hour), Encryption: e}, 100)
	if err != nil {
		t.Fatal(err)
	}
	*e.Selection.BucketKeyEnabled = true
	*u.Encryption.Selection.BucketKeyEnabled = true
	u, err = sessions.GetObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || !u.Encryption.Equal(original) {
		t.Fatal("multipart snapshot aliased", u, err)
	}
	replay := u
	replay.Encryption = e
	if _, err = sessions.ReserveObjectMultipartUpload(t.Context(), replay, 100); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed initialization accepted", err)
	}
	u, err = sessions.ClaimObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = sessions.ActivateObjectMultipartUpload(t.Context(), u.ID, "init", "native-upload"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = transfers.BeginObjectMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, "part", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if err = transfers.SettleObjectMultipartPart(t.Context(), b.AccountID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}
	changed := u
	changed.Encryption = journalEncryption(b.AccountID)
	if _, err = transfers.PrepareObjectMultipartCompletion(t.Context(), changed, "complete", 30, parts, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed preparation accepted", err)
	}
	u, err = transfers.PrepareObjectMultipartCompletion(t.Context(), u, "complete", 30, parts, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err = sessions.FinishObjectMultipartUpload(t.Context(), u.ID, u.LeaseToken, state.ObjectMultipartCompleted); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic settlement accepted", err)
	}
	if err = results.DispatchObjectMultipartCompletion(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if _, err = results.FinishObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{ETag: `"actual"`}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("missing verification settled", err)
	}
	noObjectEvent(t, st)
	if err = results.RetryObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{RecoveryCursor: "cursor", VersionsObserved: true}, "temporary", time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	u, err = sessions.ClaimObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID, "restart", state.ObjectMultipartCompleting, nil, true)
	if err != nil || !u.Encryption.Equal(original) || u.CompletionRecoveryCursor != "cursor" {
		t.Fatal("retry lost snapshot", u, err)
	}
	done, err := results.FinishObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{ETag: `"actual"`, VerifiedEncryption: original.Selection})
	if err != nil || done.State != state.ObjectMultipartCompleted || !done.Encryption.Equal(original) {
		t.Fatal(done, err)
	}
	takeObjectEvent(t, st, b.AccountID, "multipart:"+u.ID, api.ObjectEventCreated)
	raw, err := json.Marshal(done)
	if err != nil || strings.Contains(string(raw), "provider-private") {
		t.Fatal("multipart leaked private key", string(raw), err)
	}
}

func TestObjectEncryptionLegacySQLGuardsPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	c, err := st.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "write", Bytes: 10, Status: "pending", Encryption: journalEncryption(b.AccountID)}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rejected := func(query string, id string) {
		t.Helper()
		_, e := pool.Exec(ctx, query, id)
		var pg *pgconn.PgError
		if !errors.As(e, &pg) || pg.Code != "23514" {
			t.Fatalf("legacy guard missing: %v", e)
		}
	}
	rejected(`UPDATE object_upload_completions SET write_phase='dispatched' WHERE id=$1`, c.ID)
	rejected(`UPDATE object_upload_completions SET encryption_snapshot='{}' WHERE id=$1`, c.ID)
	c, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	rejected(`UPDATE object_upload_completions SET status='completed',etag='tag',write_phase='settled' WHERE id=$1`, c.ID)
	restarted := state.NewPgStore(pool)
	got, err := restarted.GetObjectUploadReceipt(ctx, b.AccountID, b.AppID, "", "subject", c.ID)
	if err != nil || !got.Encryption.Equal(c.Encryption) || got.Status != "pending" {
		t.Fatal("restart lost immutable identity", got, err)
	}
	u, err := st.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "multipart", ExpiresAt: time.Now().Add(time.Hour), Encryption: journalEncryption(b.AccountID)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	rejected(`UPDATE object_storage_multipart_uploads SET lease_token='old-worker',lease_until=now()+interval '1 minute' WHERE id=$1`, u.ID)
	rejected(`UPDATE object_storage_multipart_uploads SET encryption_snapshot='{}' WHERE id=$1`, u.ID)
	u, err = restarted.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "new-worker", state.ObjectMultipartInitiating, nil, false)
	if err != nil || u.Encryption.Empty() {
		t.Fatal("aware claimant blocked", u, err)
	}
	if err = restarted.ActivateObjectMultipartUpload(ctx, u.ID, "new-worker", "native"); err != nil {
		t.Fatal(err)
	}
}
