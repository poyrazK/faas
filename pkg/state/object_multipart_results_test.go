package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 402
func TestObjectMultipartResultsMem(t *testing.T) { multipartResultsSuite(t, state.NewMemStore()) }
func TestObjectMultipartResultsPG(t *testing.T)  { st, _ := pgStore(t); multipartResultsSuite(t, st) }

func preparedMultipartResult(t *testing.T, st accountingStore, key string, conditions ...api.ObjectWriteConditions) (state.ObjectBucket, state.ObjectMultipartUpload) {
	t.Helper()
	b, u := activeTrackedUpload(t, st, key)
	transfers := st.(state.ObjectMultipartTransferStore)
	if err := transfers.BeginObjectMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, "part", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if err := transfers.SettleObjectMultipartPart(t.Context(), b.AccountID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	u, err := st.(state.ObjectMultipartUploadStore).GetObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(conditions) > 0 {
		u.CompletionConditions = conditions[0]
	}
	u, err = transfers.PrepareObjectMultipartCompletion(t.Context(), u, "complete", 30, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return b, u
}

func TestObjectMultipartResultRejectionMem(t *testing.T) {
	multipartResultRejection(t, state.NewMemStore())
}
func TestObjectMultipartResultRejectionPG(t *testing.T) {
	st, _ := pgStore(t)
	multipartResultRejection(t, st)
}

func multipartResultRejection(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, u := preparedMultipartResult(t, st, "rejected", api.ObjectWriteConditions{IfNoneMatch: "*"})
	results := st.(state.ObjectMultipartCompletionStore)
	sessions := st.(state.ObjectMultipartUploadStore)
	if err := results.DispatchObjectMultipartCompletion(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []state.ObjectMultipartCompletionResult{{RecoveryCursor: "bad+"}, {RecoveryCursor: strings.Repeat("a", api.ObjectUploadHistoryCursorMaxBytes+1)}} {
		if err := results.RetryObjectMultipartCompletion(ctx, u, bad, "temporary", time.Second); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid recovery cursor accepted", err)
		}
	}
	if err := results.RetryObjectMultipartCompletion(ctx, u, state.ObjectMultipartCompletionResult{RecoveryCursor: "private_cursor", VersionsObserved: true}, "temporary", time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	got, err := sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || !got.CompletionDispatched || !got.CompletionVersionsObserved || got.CompletionRecoveryCursor != "private_cursor" {
		t.Fatal(got, err)
	}
	u, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "reject", got.State, got.Parts, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = results.RejectObjectMultipartCompletionResult(ctx, u, state.ObjectMultipartCompletionResult{}, "provider-private"); !errors.Is(err, state.ErrConflict) {
		t.Fatal(err)
	}
	if err = results.RejectObjectMultipartCompletionResult(ctx, u, state.ObjectMultipartCompletionResult{}, "precondition_failed"); err != nil {
		t.Fatal(err)
	}
	got, err = sessions.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || got.State != state.ObjectMultipartAborting || got.CompletionErrorCode != "precondition_failed" || !got.CompletionVersionsObserved || got.CompletionRecoveryCursor != "" {
		t.Fatal(got, err)
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Buckets[0].MultipartBytes != 30 {
		t.Fatal("rejection released unverified parts", usage, err)
	}
	status, err := st.(state.ObjectVersionInventoryStore).ObjectVersionAccountingStatus(ctx, b.AccountID, b.ID)
	if err != nil || !status.VersionsObserved {
		t.Fatal("retry/rejection forgot native observation", status, err)
	}
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "new", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("retry/rejection did not fence writes", err)
	}
	if err = st.ClaimObjectInventory(ctx, b.ID, "unsafe"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, "unsafe", 0, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatal("current-object scan reclaimed observed versions", err)
	}
}

func multipartResultsSuite(t *testing.T, st accountingStore) {
	for _, native := range []string{"", "null", "private/+?native"} {
		t.Run("native="+native, func(t *testing.T) {
			ctx := t.Context()
			b, u := preparedMultipartResult(t, st, "result")
			results := st.(state.ObjectMultipartCompletionStore)
			sessions := st.(state.ObjectMultipartUploadStore)
			proof := state.ObjectMultipartCompletionResult{ETag: `"actual-checksum-1"`, ProviderVersionID: native}
			if _, err := results.FinishObjectMultipartCompletion(ctx, u, proof); !errors.Is(err, state.ErrConflict) {
				t.Fatal("completed before durable dispatch", err)
			}
			for _, mutate := range []func(*state.ObjectMultipartUpload){func(u *state.ObjectMultipartUpload) { u.AccountID = uuid.NewString() }, func(u *state.ObjectMultipartUpload) { u.AppID = uuid.NewString() }, func(u *state.ObjectMultipartUpload) { u.BucketID = uuid.NewString() }, func(u *state.ObjectMultipartUpload) { u.LeaseToken = "stale" }, func(u *state.ObjectMultipartUpload) { u.Key = "other" }, func(u *state.ObjectMultipartUpload) { u.SizeBytes++ }} {
				bad := u
				mutate(&bad)
				if err := results.DispatchObjectMultipartCompletion(ctx, bad); err == nil {
					t.Fatal("foreign or stale dispatch accepted")
				}
			}
			if err := results.DispatchObjectMultipartCompletion(ctx, u); err != nil {
				t.Fatal(err)
			}
			if err := sessions.FinishObjectMultipartUpload(ctx, u.ID, u.LeaseToken, state.ObjectMultipartCompleted); !errors.Is(err, state.ErrConflict) {
				t.Fatal("legacy finish discarded a dispatched result", err)
			}
			for _, bad := range []state.ObjectMultipartCompletionResult{{}, {ETag: " "}, {ETag: "bad\n"}, {ETag: `"tag"`, ProviderVersionID: "bad\n"}, {ETag: `"tag"`, RecoveryCursor: "cursor"}} {
				if _, err := results.FinishObjectMultipartCompletion(ctx, u, bad); !errors.Is(err, state.ErrConflict) {
					t.Fatal("invalid result committed", bad, err)
				}
			}
			completed, err := results.FinishObjectMultipartCompletion(ctx, u, proof)
			if err != nil || completed.State != state.ObjectMultipartCompleted || completed.CompletionETag != proof.ETag || completed.LeaseToken != "" || !completed.CompletionDispatched {
				t.Fatal(completed, err)
			}
			if native == "" && completed.CompletionVersionID != "" || native == "null" && completed.CompletionVersionID != "null" || native != "" && native != "null" && (!state.ValidObjectVersionID(completed.CompletionVersionID) || completed.CompletionVersionID == native) {
				t.Fatal("incorrect public identity", completed)
			}
			if native != "" {
				got, e := st.(state.ObjectVersionReferenceStore).ResolveObjectVersion(ctx, b.AccountID, b.ID, u.Key, completed.CompletionVersionID)
				if e != nil || got != native {
					t.Fatal("completion did not atomically publish its identity", got, e)
				}
			}
			proof.ETag = `"mutated"`
			if _, err = results.FinishObjectMultipartCompletion(ctx, u, proof); !errors.Is(err, state.ErrConflict) {
				t.Fatal("terminal result overwritten", err)
			}
			usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
			if err != nil || usage.Buckets[0].MultipartBytes != 0 || state.SummarizeObjectUsage(usage, accountingPolicy(), time.Now()).CapacityBytes != 30 {
				t.Fatal("completion lost the final object reservation", usage, err)
			}
		})
	}
}

func TestObjectMultipartResultRecoveryRestartPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, u := preparedMultipartResult(t, st, "restart")
	if err := st.DispatchObjectMultipartCompletion(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.RetryObjectMultipartCompletion(ctx, u, state.ObjectMultipartCompletionResult{RecoveryCursor: "bound_cursor", VersionsObserved: true}, "temporary", time.Second); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	got, err := restarted.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil || got.CompletionRecoveryCursor != "bound_cursor" || !got.CompletionVersionsObserved || !got.CompletionDispatched || got.LeaseToken != "" || got.State != state.ObjectMultipartCompleting {
		t.Fatal(got, err)
	}
	usage, err := restarted.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Buckets[0].MultipartBytes != 30 {
		t.Fatal("uncertain completion released parts", usage, err)
	}
	if err = restarted.AdmitObjectURL(ctx, b.AccountID, b.ID, "new", 1, true, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("lost observation fence", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET retry_at=now()-interval '1 second' WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	u, err = restarted.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "restart-owner", state.ObjectMultipartCompleting, got.Parts, true)
	if err != nil || u.CompletionRecoveryCursor != "bound_cursor" {
		t.Fatal(u, err)
	}
	completed, err := restarted.FinishObjectMultipartCompletion(ctx, u, state.ObjectMultipartCompletionResult{ETag: `"historical"`, ProviderVersionID: "private-old"})
	if err != nil || completed.CompletionRecoveryCursor != "" || !completed.CompletionVersionsObserved {
		t.Fatal(completed, err)
	}
	if err = st.RetryObjectMultipartCompletion(ctx, got, state.ObjectMultipartCompletionResult{}, "temporary", time.Second); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale owner changed recovered result", err)
	}
}

func TestObjectMultipartResultMigrationPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	_, u := preparedMultipartResult(t, st, "rollout")
	raw, err := migrations.FS.ReadFile("20261002150000001_object_multipart_completion_results.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetObjectMultipartUpload(ctx, u.AccountID, u.AppID, u.BucketID, u.ID)
	if err != nil || !got.CompletionDispatched || got.CompletionETag != "" {
		t.Fatal("rollout forgot potentially dispatched intent", got, err)
	}
	for _, query := range []string{
		`UPDATE object_storage_multipart_uploads SET completion_dispatched=false WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET completion_etag='injected' WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET state='completed',completion_etag='   ' WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET completion_recovery_cursor='bad+' WHERE id=$1`,
		`UPDATE object_storage_multipart_uploads SET state='completed',completion_etag='actual',completion_version_id='00000000-0000-4000-8000-000000000001' WHERE id=$1`,
	} {
		if _, err = pool.Exec(ctx, query, u.ID); err == nil {
			t.Fatal("invalid SQL result accepted", query)
		}
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err == nil || !strings.Contains(err.Error(), "Cannot discard multipart completion") {
		t.Fatal("rollback discarded durable dispatch", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	completed, err := st.FinishObjectMultipartCompletion(ctx, got, state.ObjectMultipartCompletionResult{ETag: `"actual"`, ProviderVersionID: "native"})
	if err != nil {
		t.Fatal(completed, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET completion_etag='changed' WHERE id=$1`, u.ID); err == nil {
		t.Fatal("SQL changed terminal result")
	}
}
