package runfiles_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestStoredFileTransferAuthorizationRetentionAndCleanup(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "file-service@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := age.GenerateX25519Identity()
	svc := runfiles.Service{Store: store, Storage: backend, Recipients: []*age.X25519Recipient{identity.Recipient()}, Identities: []*age.X25519Identity{identity}}
	principal := uuid.NewString()
	access := runfiles.Access{Account: acct, Principal: &principal}
	data := bytes.Repeat([]byte("run-file"), 300000)
	hash := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	req := api.CreateExecutionFileRequest{SizeBytes: int64(len(data)), SHA256: digest}
	prepared, err := svc.Prepare(ctx, access, req)
	if err != nil || !prepared.UploadRequired {
		t.Fatal(prepared, err)
	}
	if _, err = store.ExecutionFileByID(ctx, acct.ID, prepared.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("prepare reserved metadata", err)
	}
	published, err := svc.Upload(ctx, access, prepared.ID, int64(len(data)), digest, io.NopCloser(bytes.NewReader(data)))
	if err != nil || published.Status != "ready" {
		t.Fatal(published, err)
	}
	different := uuid.NewString()
	if _, err = svc.Get(ctx, runfiles.Access{Account: acct, Principal: &different}, prepared.ID); err == nil {
		t.Fatal("another key family read metadata")
	}
	_, body, err := svc.Open(ctx, access, prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	received, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || !bytes.Equal(data, received) {
		t.Fatal("content", err)
	}
	again, err := svc.Prepare(ctx, access, req)
	if err != nil || again.ID != prepared.ID || again.UploadRequired {
		t.Fatal("stable ready identity", err)
	}
	now := time.Now().UTC()
	if err = store.RevokeExecutionFile(ctx, acct.ID, prepared.ID, access.Principal, false, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.Open(ctx, access, prepared.ID); err == nil {
		t.Fatal("revoked content is downloadable")
	}
	if n, err := svc.Collect(ctx, now.Add(api.ExecutionFileGCGrace+time.Second)); err != nil || n != 1 {
		t.Fatal("cleanup", n, err)
	}
	again, err = svc.Prepare(ctx, access, req)
	if err != nil || again.ID != prepared.ID || !again.UploadRequired {
		t.Fatal("identity changed after GC", err)
	}
}
