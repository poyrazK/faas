package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreJobRegistryCredentialsRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "job-credential-state@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := m.JobCreate(ctx, acct.ID, "private", "batch", "registry.example/worker:latest", nil, 128, 30, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	ciphertext := []byte("sealed-password")
	if err := m.UpsertJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example", "robot", ciphertext); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := m.GetJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Username != "robot" || string(got.PasswordEncrypted) != string(ciphertext) {
		t.Fatalf("credential = %+v", got)
	}
	got.PasswordEncrypted[0] = 'X'
	unchanged, err := m.GetJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example")
	if err != nil || string(unchanged.PasswordEncrypted) != string(ciphertext) {
		t.Fatalf("ciphertext was not copied: %+v, %v", unchanged, err)
	}
	created := got.CreatedAt
	if err := m.MarkJobRegistryCredentialUsed(ctx, acct.ID, job.ID, "registry.example"); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	used, err := m.GetJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example")
	if err != nil || used.LastUsedAt == nil || !used.CreatedAt.Equal(created) {
		t.Fatalf("after MarkUsed = %+v, err=%v", used, err)
	}
	if err := m.UpsertJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example", "robot-2", []byte("sealed-2")); err != nil {
		t.Fatalf("Upsert replacement: %v", err)
	}
	replaced, err := m.GetJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example")
	if err != nil || replaced.Username != "robot-2" || replaced.LastUsedAt == nil {
		t.Fatalf("replacement = %+v, err=%v", replaced, err)
	}

	other, err := m.CreateAccount(ctx, "job-credential-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("Create other account: %v", err)
	}
	if _, err := m.GetJobRegistryCredential(ctx, other.ID, job.ID, "registry.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account Get = %v, want ErrNotFound", err)
	}
	if err := m.MarkJobRegistryCredentialUsed(ctx, other.ID, job.ID, "registry.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account MarkUsed = %v, want ErrNotFound", err)
	}
	if err := m.UpsertJobRegistryCredential(ctx, other.ID, job.ID, "registry.example", "bad", []byte("bad")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account Upsert = %v, want ErrNotFound", err)
	}
	if err := m.MarkJobRegistryCredentialUsed(ctx, acct.ID, job.ID, "missing.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing MarkUsed = %v, want ErrNotFound", err)
	}
}

func TestMemStoreJobRegistryCredentialsAccountCascade(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "job-credential-cascade@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := m.JobCreate(ctx, acct.ID, "private", "batch", "registry.example/worker:latest", nil, 128, 30, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if err := m.UpsertJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example", "robot", []byte("sealed")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := m.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := m.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if _, err := m.GetJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after account deletion = %v, want ErrNotFound", err)
	}
}
