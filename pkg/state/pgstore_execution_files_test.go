//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgExecutionStoredFileReservationPublicationAndCleanup(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct := pgExecutionAccount(t, store, ctx, "stored-file-"+uuid.NewString())
	now := time.Now().UTC().Truncate(time.Microsecond)
	upload := fileUploadFixture(acct.ID, 4<<20, now)
	if _, _, err := store.ClaimExecutionFileUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}
	ready, err := store.FinishExecutionFileUpload(ctx, acct.ID, upload.ID, *upload.Token, now.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	p := pgExecutionParams(t, acct.ID, now.Add(2*time.Millisecond), 0, "sealed")
	p.Request.StoredInputs = []runfile.Ref{{Descriptor: runfile.Descriptor{ID: ready.ID, Path: "data.bin", SizeBytes: ready.SizeBytes, SHA256: ready.SHA256}, StorageKey: ready.StorageKey, PrivateKey: "fixture-private-key"}}
	output := fileUploadFixture(acct.ID, 0, now)
	p.Request.OutputFiles = []string{"copy.bin"}
	p.Request.Limits.MaxArtifactBytes = 4 << 20
	p.Request.StoredOutputs = []runfile.Ref{{Descriptor: runfile.Descriptor{ID: output.ID, Path: "copy.bin"}, StorageKey: output.StorageKey, SealedKey: output.SealedKey, PrivateKey: "fixture-private-key"}}
	run, err := store.CreateExecution(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if run.InputFileBytes != ready.SizeBytes || run.Limits.MaxArtifactBytes != 4<<20 {
		t.Fatal("persisted file budgets", run.InputFileBytes, run.Limits.MaxArtifactBytes)
	}
	intent, err := store.ExecutionFileByID(ctx, acct.ID, output.ID)
	if err != nil || intent.Status != "pending" || intent.ExecutionID == nil || *intent.ExecutionID != run.ID {
		t.Fatal("output intent missing", err)
	}
	if err = store.RevokeExecutionFile(ctx, acct.ID, ready.ID, nil, true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.ClaimExpiredExecutionFiles(ctx, now.Add(10*time.Minute), 32)
	if err != nil || len(candidates) != 0 {
		t.Fatal("collected files of active Run", err)
	}
	if _, err = store.RequestExecutionCancellation(ctx, acct.ID, run.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ClaimExpiredExecutionFiles(ctx, now.Add(25*time.Hour), 32)
	if err != nil || len(candidates) != 2 {
		t.Fatal("expired Run files not claimed", len(candidates), err)
	}
	for _, f := range candidates {
		if err = store.DeleteExecutionFileMetadata(ctx, acct.ID, f.ID, *f.Token); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.ExecutionFileByID(ctx, acct.ID, output.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("metadata retained after physical GC acknowledgement", err)
	}
	var links int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM execution_file_links WHERE execution_id=$1", run.ID).Scan(&links); err != nil || links != 0 {
		t.Fatal("stale file links", links, err)
	}
}

func TestPgExecutionContractValidationPersistsWithoutChangingExecutionStatus(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct := pgExecutionAccount(t, store, ctx, "contract-"+uuid.NewString())
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := pgExecutionParams(t, acct.ID, now, 0, "sealed-contract")
	row, err := store.CreateExecution(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecution(ctx, "contract-test", now.Add(time.Millisecond), time.Minute)
	if err != nil || claim.ID != row.ID {
		t.Fatal("claim", err)
	}
	if _, err = store.MarkExecutionRunning(ctx, row.ID, *claim.LeaseToken, now.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteExecution(ctx, state.CompleteExecutionParams{ID: row.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: []byte(`{"debug":"retained"}`), ResultValidation: &api.ExecutionResultValidation{Status: "invalid", Message: "Run result does not match result_schema"}, FinishedAt: now.Add(3 * time.Millisecond)})
	if err != nil || completed.Status != api.ExecutionStatusSucceeded || completed.ResultValidation == nil || completed.ResultValidation.Status != "invalid" {
		t.Fatal("completion", err)
	}
	read, err := store.ExecutionByID(ctx, acct.ID, row.ID)
	var result struct{ Debug string }
	if err != nil || read.ResultValidation == nil || read.ResultValidation.Status != "invalid" || json.Unmarshal(read.Result, &result) != nil || result.Debug != "retained" {
		t.Fatal("receipt", err)
	}
	var payloads, charges int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM execution_payloads WHERE execution_id=$1),(SELECT count(*) FROM execution_usage_ledger WHERE execution_id=$1)`, row.ID).Scan(&payloads, &charges); err != nil || payloads != 0 || charges != 1 {
		t.Fatal("terminal atomicity", payloads, charges, err)
	}
}

func TestPgExecutionStoredFileConcurrentQuota(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "file-quota-"+uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	results := make(chan error, 12)
	for range 12 {
		go func() {
			_, _, err := store.ClaimExecutionFileUpload(ctx, fileUploadFixture(acct.ID, 8<<20, now))
			results <- err
		}()
	}
	success := 0
	for range 12 {
		err := <-results
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
		t.Fatalf("concurrent uploads reserved %d slots; want 8", success)
	}
}

func TestPgExecutionStoredFileAccountDeletionPreservesPhysicalCleanup(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct := pgExecutionAccount(t, store, ctx, "file-owner-deletion-"+uuid.NewString())
	exerciseFileOwnerDeletion(t, store, acct.ID)
}
