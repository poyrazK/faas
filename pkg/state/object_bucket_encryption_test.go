package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 561
func TestObjectBucketEncryptionMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	bucketEncryptionSuite(t, m, nil, func(string) { now = now.Add(api.ObjectBucketEncryptionLease + time.Second) })
}

func TestObjectBucketEncryptionPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	bucketEncryptionSuite(t, st, pool, func(bucket string) {
		if _, err := pool.Exec(ctx, `UPDATE object_bucket_encryption SET lease_until=clock_timestamp()-interval '1 second',retry_at=clock_timestamp() WHERE bucket_id=$1`, bucket); err != nil {
			t.Fatal(err)
		}
	})
}

func readyBucketDefault(t *testing.T, st state.ObjectBucketEncryptionStore, b state.ObjectBucket, e state.ObjectEncryptionSnapshot) state.ObjectBucketEncryption {
	t.Helper()
	j, err := st.RequestObjectBucketEncryption(t.Context(), b.AccountID, b.AppID, b.ID, e)
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = st.ClaimObjectBucketEncryption(t.Context(), b.ID, uuid.NewString())
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = st.DispatchObjectBucketEncryption(t.Context(), b.ID, j.Token)
	if err != nil {
		t.Fatal(j, err)
	}
	j, err = st.FinishObjectBucketEncryption(t.Context(), b.ID, j.Token, e)
	if err != nil || j.State != "ready" {
		t.Fatal(j, err)
	}
	return j
}

func bucketEncryptionSuite(t *testing.T, st accountingStore, pool *pgxpool.Pool, expire func(string)) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	defaults := st.(state.ObjectBucketEncryptionStore)
	writes := st.(state.ObjectTrackedGatewayUploadStore)
	sessions := st.(state.ObjectMultipartUploadStore)
	e := journalEncryption(b.AccountID)
	e.Selection.Context = ""
	j, err := defaults.GetObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || j.Revision != 0 || j.State != "ready" {
		t.Fatal(j, err)
	}
	if _, err = defaults.GetObjectBucketEncryption(ctx, uuid.NewString(), b.AppID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign policy disclosed", err)
	}
	bad := e.Clone()
	bad.Selection.Context = journalEncryption(b.AccountID).Selection.Context
	if _, err = defaults.RequestObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatal("context default accepted", err)
	}
	attempt := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "writer", Key: "before", Bytes: 2, Status: "pending"}
	before, err := writes.BeginTrackedGatewayUpload(ctx, attempt, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	j, err = defaults.RequestObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID, e)
	if err != nil || j.Revision != 1 || j.State != "waiting" || !j.Encryption.Empty() {
		t.Fatal(j, err)
	}
	replay, err := defaults.RequestObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID, e)
	if err != nil || replay.Revision != 1 {
		t.Fatal(replay, err)
	}
	if _, err = defaults.RequestObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID, state.ObjectEncryptionSnapshot{}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("pending policy replaced", err)
	}
	attempt.ID, attempt.Key = uuid.NewString(), "pending"
	if _, err = writes.BeginTrackedGatewayUpload(ctx, attempt, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("implicit write passed unresolved default", err)
	}
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "legacy", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy write passed unresolved default", err)
	}
	if _, err = st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "delete", "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("configuration lost to bucket deletion", err)
	}
	if _, err = writes.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, before.ID); err != nil {
		t.Fatal("accepted write was reconfigured", err)
	}
	explicit := attempt
	explicit.ID = uuid.NewString()
	explicit.Key = "explicit"
	explicit.Encryption = state.ObjectEncryptionSnapshot{AccountID: uuid.MustParse(b.AccountID).String(), Selection: api.ObjectEncryption{Algorithm: "AES256"}}
	c, err := writes.BeginTrackedGatewayUpload(ctx, explicit, accountingPolicy())
	if err != nil || c.EncryptionDefaultRevision != 0 || !c.Encryption.Equal(explicit.Encryption) {
		t.Fatal(c, err)
	}
	j, err = defaults.ClaimObjectBucketEncryption(ctx, b.ID, "old")
	if err != nil {
		t.Fatal(j, err)
	}
	if _, err = defaults.ClaimObjectBucketEncryption(ctx, b.ID, "competing"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("duplicate lease", err)
	}
	if _, err = defaults.DispatchObjectBucketEncryption(ctx, b.ID, "old"); err != nil {
		t.Fatal(err)
	}
	expire(b.ID)
	if pool != nil {
		defaults = state.NewPgStore(pool)
	}
	j, err = defaults.ClaimObjectBucketEncryption(ctx, b.ID, "restart")
	if err != nil || !j.Dispatched || !j.DesiredEncryption.Equal(e) {
		t.Fatal("restart forgot dispatched identity", j, err)
	}
	if _, err = defaults.FinishObjectBucketEncryption(ctx, b.ID, "old", e); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker published", err)
	}
	if _, err = defaults.FinishObjectBucketEncryption(ctx, b.ID, "restart", explicit.Encryption); !errors.Is(err, state.ErrConflict) {
		t.Fatal("mismatched native configuration published", err)
	}
	j, err = defaults.FinishObjectBucketEncryption(ctx, b.ID, "restart", e)
	if err != nil || j.Revision != 1 || !j.Encryption.Equal(e) {
		t.Fatal(j, err)
	}
	*j.Encryption.Selection.BucketKeyEnabled = true
	j, err = defaults.GetObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || !j.Encryption.Equal(e) {
		t.Fatal("snapshot aliases a caller", j, err)
	}
	attempt.ID, attempt.Key = uuid.NewString(), "default"
	c, err = writes.BeginTrackedGatewayUpload(ctx, attempt, accountingPolicy())
	if err != nil || c.EncryptionDefaultRevision != 1 || !c.Encryption.Equal(e) {
		t.Fatal("write failed to capture default", c, err)
	}
	copyAttempt := attempt
	copyAttempt.ID = uuid.NewString()
	copyAttempt.Key = "copy"
	copyAttempt.SourceKey = "source"
	copyAttempt.SourceETag = `"source"`
	copied, err := st.(state.ObjectTrackedGatewayCopyStore).BeginTrackedGatewayCopy(ctx, copyAttempt, accountingPolicy())
	if err != nil || copied.EncryptionDefaultRevision != 1 || !copied.Encryption.Equal(e) {
		t.Fatal("copy dropped default", copied, err)
	}
	multipartCandidate := fixedMultipartCandidate(b, "multipart")
	u, err := st.(state.ObjectFixedMultipartAdmissionStore).ReserveAdmittedObjectMultipartUpload(ctx, multipartCandidate, 100, accountingPolicy())
	if err != nil || u.EncryptionDefaultRevision != 1 || !u.Encryption.Equal(e) {
		t.Fatal("multipart dropped default", u, err)
	}
	unknown := state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "unknown", ExpiresAt: time.Now().Add(time.Hour)}
	unknown, err = sessions.ReserveObjectMultipartUpload(ctx, unknown, 100)
	if err != nil || unknown.EncryptionDefaultRevision != 1 || !unknown.Encryption.Equal(e) {
		t.Fatal("S3 multipart dropped default", unknown, err)
	}
	bucketDefaultRouteAndURL(t, st, b, e)
	if pool != nil {
		assertBucketDefaultSQLFences(t, pool, b, c)
	}
	j = readyBucketDefault(t, defaults, b, state.ObjectEncryptionSnapshot{})
	if j.Revision != 2 || !j.Encryption.Empty() {
		t.Fatal("clear discarded tombstone", j)
	}
	multipartCandidate.ID = uuid.NewString()
	replayUpload, err := st.(state.ObjectFixedMultipartAdmissionStore).ReserveAdmittedObjectMultipartUpload(ctx, multipartCandidate, 100, accountingPolicy())
	if err != nil || replayUpload.ID != u.ID || replayUpload.EncryptionDefaultRevision != 1 || !replayUpload.Encryption.Equal(e) {
		t.Fatal("creation replay changed accepted encryption", replayUpload, err)
	}
	c, err = writes.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag, c.VerifiedEncryption = "completed", `"result"`, e.Clone().Selection
	forged := c
	forged.EncryptionDefaultRevision = 2
	if _, err = writes.FinishTrackedObjectUpload(ctx, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatal("provenance changed during completion", err)
	}
	done, err := writes.FinishTrackedObjectUpload(ctx, c)
	if err != nil || done.EncryptionDefaultRevision != 1 || !done.Encryption.Equal(e) {
		t.Fatal("completion reread current default", done, err)
	}
	attempt.ID, attempt.Key = uuid.NewString(), "cleared"
	c, err = writes.BeginTrackedGatewayUpload(ctx, attempt, accountingPolicy())
	if err != nil || !c.Encryption.Empty() || c.EncryptionDefaultRevision != 0 {
		t.Fatal("clear failed to remove owned default", c, err)
	}
}

func bucketDefaultRouteAndURL(t *testing.T, st accountingStore, b state.ObjectBucket, e state.ObjectEncryptionSnapshot) {
	t.Helper()
	ctx := t.Context()
	routes := st.(state.ObjectUploadRouteStore)
	route, err := routes.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Name: "defaulted", MaxBytes: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	attempt := state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "route", Key: "route", Bytes: 5, Status: "pending", IdempotencyKey: "once", RequestFingerprint: "fingerprint", RuntimeSinglePutLimit: 4}
	tracked := st.(state.ObjectTrackedUploadStore)
	if _, _, err = tracked.BeginTrackedObjectUpload(ctx, attempt, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("encrypted default ignored runtime PUT limit", err)
	}
	attempt.RuntimeSinglePutLimit = 10
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := tracked.BeginTrackedObjectUpload(ctx, attempt, accountingPolicy())
			if err != nil {
				t.Error(err)
				return
			}
			if c.EncryptionDefaultRevision != 1 || !c.Encryption.Equal(e) {
				t.Error("route lost default")
			}
			ids <- c.ID
		}()
	}
	wg.Wait()
	close(ids)
	for id := range ids {
		if id != attempt.ID {
			t.Fatal("route replay duplicated", id)
		}
	}
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	size := int64(3)
	credential := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "recipient", Label: "default", Permission: state.ObjectBucketPermissionWrite, Status: state.ObjectS3CredentialStatusActive, URL: &state.ObjectURLCapability{Request: api.ObjectSignRequest{Method: "PUT", Key: "url", ExpiresIn: 300, SizeBytes: &size}, ReceiptID: uuid.NewString(), ExpiresAt: time.Now().Add(4 * time.Minute).UTC().Truncate(time.Microsecond)}}
	receipt := state.ObjectUploadCompletion{ID: credential.URL.ReceiptID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: credential.ID, Key: "url", Bytes: size, Status: "pending"}
	saved, receipt, err := st.(state.ObjectURLCapabilityStore).IssueObjectURLCredential(ctx, credential, receipt, accountingPolicy())
	if err != nil || receipt.EncryptionDefaultRevision != 1 || !receipt.Encryption.Equal(e) || saved.URL.Request.Encryption == nil || saved.URL.Request.Encryption.KeyID != e.Selection.KeyID {
		t.Fatal("URL signature authority dropped default", saved, receipt, err)
	}
}

func assertBucketDefaultSQLFences(t *testing.T, pool *pgxpool.Pool, b state.ObjectBucket, c state.ObjectUploadCompletion) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes) VALUES($1,repeat('a',64),1)`,
		`UPDATE object_bucket_encryption SET revision=revision+1 WHERE bucket_id=$1`,
		`DELETE FROM object_bucket_encryption WHERE bucket_id=$1`,
		`INSERT INTO object_storage_multipart_uploads(id,account_id,app_id,bucket_id,object_key,expires_at) SELECT gen_random_uuid(),account_id,app_id,id,'legacy',clock_timestamp()+interval '1 hour' FROM object_buckets WHERE id=$1`,
		`INSERT INTO object_upload_completions(id,account_id,app_id,bucket_id,subject_id,object_key,bytes,status,write_phase) SELECT gen_random_uuid(),account_id,app_id,id,'legacy','legacy',1,'pending','prepared' FROM object_buckets WHERE id=$1`,
	} {
		_, err := pool.Exec(t.Context(), q, b.ID)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Fatal("database accepted stale writer", q, err)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE object_upload_completions SET encryption_default_revision=2 WHERE id=$1`, c.ID); err == nil {
		t.Fatal("database accepted changed provenance")
	}
	body, err := migrations.FS.ReadFile("20261004090600531_object_bucket_encryption_defaults.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(body), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context()) //nolint:errcheck
	if _, err = tx.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "Clear bucket defaults") {
		t.Fatal("rollback discarded defaults", err)
	}
}

// Configuration must finish while admissions hold the account lock: taking
// that lock after the bucket lock would create a deadlock with upload work.
func TestObjectBucketEncryptionPGAccountLockAndRollback(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	defaults := state.ObjectBucketEncryptionStore(st)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, b.AccountID); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	e := journalEncryption(b.AccountID)
	e.Selection.Context = ""
	if _, err = defaults.RequestObjectBucketEncryption(bounded, b.AccountID, b.AppID, b.ID, e); err != nil {
		t.Fatal("configuration waited for admission's account lock", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := defaults.ClaimObjectBucketEncryption(ctx, b.ID, "finish")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = defaults.DispatchObjectBucketEncryption(ctx, b.ID, j.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = defaults.FinishObjectBucketEncryption(ctx, b.ID, j.Token, e); err != nil {
		t.Fatal(err)
	}
	readyBucketDefault(t, defaults, b, state.ObjectEncryptionSnapshot{})
	body, err := migrations.FS.ReadFile("20261004090600531_object_bucket_encryption_defaults.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(body), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	rollback, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback.Rollback(ctx) //nolint:errcheck
	if _, err = rollback.Exec(ctx, down); err != nil {
		t.Fatal("cleared and drained rollback failed", err)
	}
}
