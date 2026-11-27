package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/state"
)

func fileUploadFixture(account string, size int64, now time.Time) state.ExecutionFile {
	id, token := uuid.NewString(), uuid.NewString()
	lease := now.Add(api.ExecutionFileUploadTimeout)
	return state.ExecutionFile{ID: id, AccountID: account, Kind: "upload", Status: "uploading", SizeBytes: size, SHA256: "sha256:" + strings.Repeat("a", 64), StorageKey: runfile.Key(account, id, token), SealedKey: []byte("sealed-key-fixture"), Token: &token, LeaseExpiresAt: &lease, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(api.ExecutionFileRetention)}
}
func TestMemStoredFilesConcurrentQuotaAndGC(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "file-quota@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			_, _, err := store.ClaimExecutionFileUpload(ctx, fileUploadFixture(acct.ID, 8<<20, now))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var quota *state.ExecutionFileQuotaError
		if !errors.As(err, &quota) {
			t.Fatal(err)
		}
	}
	if success != 8 {
		t.Fatalf("admitted %d uploads, want 8", success)
	}
	expired := now.Add(api.ExecutionFileUploadTimeout + api.ExecutionFileGCGrace + time.Second)
	files, err := store.ClaimExpiredExecutionFiles(ctx, expired, 32)
	if err != nil || len(files) != 8 {
		t.Fatal(len(files), err)
	}
	if _, _, err = store.ClaimExecutionFileUpload(ctx, fileUploadFixture(acct.ID, 1, expired)); err == nil {
		t.Fatal("GC claim released quota before deletion")
	}
	for _, f := range files {
		if err = store.DeleteExecutionFileMetadata(ctx, acct.ID, f.ID, *f.Token); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = store.ClaimExecutionFileUpload(ctx, fileUploadFixture(acct.ID, 1, expired)); err != nil {
		t.Fatal("deletion did not release quota", err)
	}
}
func TestMemStoredFilesImmutableUploadAndLiveRunProtection(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := executionTestAccount(t, store, "stored-input")
	now := time.Now().UTC()
	f := fileUploadFixture(acct.ID, 8<<20, now)
	if _, _, err := store.ClaimExecutionFileUpload(ctx, f); err != nil {
		t.Fatal(err)
	}
	ready, err := store.FinishExecutionFileUpload(ctx, acct.ID, f.ID, *f.Token, now.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	retry := fileUploadFixture(acct.ID, ready.SizeBytes, now.Add(2*time.Millisecond))
	retry.ID = f.ID
	retry.StorageKey = runfile.Key(acct.ID, f.ID, *retry.Token)
	replayed, replay, err := store.ClaimExecutionFileUpload(ctx, retry)
	if err != nil || !replay || replayed.StorageKey != f.StorageKey {
		t.Fatal("immutable replay", replay, err)
	}
	p := executionTestParams(t, acct.ID, now.Add(3*time.Millisecond), 0, "sealed")
	p.Request.StoredInputs = []runfile.Ref{{Descriptor: runfile.Descriptor{ID: f.ID, Path: "data.bin", SizeBytes: ready.SizeBytes, SHA256: ready.SHA256}, StorageKey: ready.StorageKey, PrivateKey: "internal-key-fixture"}}
	row, err := store.CreateExecution(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if row.InputFileBytes != f.SizeBytes {
		t.Fatal("input accounting", row.InputFileBytes)
	}
	if err = store.RevokeExecutionFile(ctx, acct.ID, f.ID, nil, true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	files, err := store.ClaimExpiredExecutionFiles(ctx, now.Add(10*time.Minute), 32)
	if err != nil || len(files) != 0 {
		t.Fatal("GC collected input of a live Run", len(files), err)
	}
	p.AdmittedAt = now.Add(2 * time.Second)
	p.DeadlineAt = p.AdmittedAt.Add(time.Duration(p.Request.Limits.TimeoutMS) * time.Millisecond)
	if _, err = store.CreateExecution(ctx, p); !errors.Is(err, state.ErrExecutionFileNotReady) {
		t.Fatal("revoked input was admitted", err)
	}
}

type fileOwnerDeletionStore interface {
	state.ExecutionFileStore
	MarkAccountDeletionPending(context.Context, string, ...state.AccountDeletionOption) error
	DeleteAccount(context.Context, string) error
}

func exerciseFileOwnerDeletion(t *testing.T, store fileOwnerDeletionStore, account string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	f := fileUploadFixture(account, 100, now)
	if _, _, err := store.ClaimExecutionFileUpload(ctx, f); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishExecutionFileUpload(ctx, account, f.ID, *f.Token, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, account); err != nil {
		t.Fatal("file ledger blocked account deletion", err)
	}
	if _, err := store.ExecutionFileByID(ctx, account, f.ID); err != nil {
		t.Fatal("account deletion lost the object cleanup record", err)
	}
	files, err := store.ClaimExpiredExecutionFiles(ctx, now.Add(api.ExecutionFileGCGrace+time.Second), 32)
	if err != nil || len(files) != 1 {
		t.Fatal("deleted owner was not collected before TTL", len(files), err)
	}
	if err = store.DeleteExecutionFileMetadata(ctx, account, f.ID, *files[0].Token); err != nil {
		t.Fatal(err)
	}
}
func TestMemStoredFilesAccountDeletionPreservesPhysicalCleanup(t *testing.T) {
	store := state.NewMemStore()
	acct := executionTestAccount(t, store, "file-owner-deletion")
	exerciseFileOwnerDeletion(t, store, acct.ID)
}
