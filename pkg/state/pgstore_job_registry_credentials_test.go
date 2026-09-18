package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreJobRegistryCredentialsRoundTrip(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "registry-credentials")
	sealed := []byte("sealed-password")
	if err := s.UpsertJobRegistryCredential(ctx, job.AccountID, job.ID, "registry.example", "robot", sealed); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := s.GetJobRegistryCredential(ctx, job.AccountID, job.ID, "registry.example")
	if err != nil || got.Username != "robot" || string(got.PasswordEncrypted) != string(sealed) {
		t.Fatalf("Get = %+v, err=%v", got, err)
	}
	if err := s.MarkJobRegistryCredentialUsed(ctx, job.AccountID, job.ID, "registry.example"); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	if got, err = s.GetJobRegistryCredential(ctx, job.AccountID, job.ID, "registry.example"); err != nil || got.LastUsedAt == nil {
		t.Fatalf("Get after MarkUsed = %+v, err=%v", got, err)
	}
	if err := s.UpsertJobRegistryCredential(ctx, job.AccountID, job.ID, "aaa.example", "robot-2", []byte("sealed-2")); err != nil {
		t.Fatalf("Upsert second registry: %v", err)
	}
	list, err := s.ListJobRegistryCredentials(ctx, job.AccountID, job.ID)
	if err != nil || len(list) != 2 || list[0].Registry != "aaa.example" || list[1].Registry != "registry.example" {
		t.Fatalf("List = %+v, err=%v", list, err)
	}
	if count, exists, err := s.JobRegistryCredentialQuotaCheck(ctx, job.AccountID, job.ID, "registry.example"); err != nil || count != 2 || !exists {
		t.Fatalf("quota existing = %d/%v, err=%v", count, exists, err)
	}
	if count, exists, err := s.JobRegistryCredentialQuotaCheck(ctx, job.AccountID, job.ID, "new.example"); err != nil || count != 2 || exists {
		t.Fatalf("quota new = %d/%v, err=%v", count, exists, err)
	}
	if err := s.DeleteJobRegistryCredential(ctx, job.AccountID, job.ID, "aaa.example"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.DeleteJobRegistryCredential(ctx, job.AccountID, job.ID, "aaa.example"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Delete missing = %v, want ErrNotFound", err)
	}
	if _, err := s.GetJobRegistryCredential(ctx, job.AccountID, job.ID, "missing.example"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	if err := s.MarkJobRegistryCredentialUsed(ctx, job.AccountID, job.ID, "missing.example"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("MarkUsed missing = %v, want ErrNotFound", err)
	}

	otherAccount := "00000000-0000-0000-0000-000000000001"
	if rows, err := s.ListJobRegistryCredentials(ctx, otherAccount, job.ID); err != nil || len(rows) != 0 {
		t.Fatalf("cross-account List = %d rows, err=%v", len(rows), err)
	}
}
