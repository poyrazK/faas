package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMemStoreMigrationLeaseLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	now := time.Now().UTC()
	lease := MigrationLease{
		InstanceID:     "instance-lease-1",
		LeaseToken:     "lease-token-1",
		SourceNodeID:   "node-a",
		CreatedAt:      now,
		LeaseExpiresAt: now.Add(time.Hour),
		Pending:        true,
	}

	if err := store.ReserveMigrationLease(ctx, lease); err != nil {
		t.Fatalf("ReserveMigrationLease: %v", err)
	}
	got, err := store.GetMigrationLease(ctx, lease.InstanceID, lease.LeaseToken)
	if err != nil {
		t.Fatalf("GetMigrationLease: %v", err)
	}
	if got.SourceNodeID != lease.SourceNodeID || !got.Pending {
		t.Fatalf("initial lease = %+v, want source=%q pending", got, lease.SourceNodeID)
	}
	if err := store.CompleteMigrationLease(ctx, lease.LeaseToken, "mem-key", "vmstate-key"); err != nil {
		t.Fatalf("CompleteMigrationLease: %v", err)
	}
	got, err = store.GetMigrationLeaseByToken(ctx, lease.LeaseToken)
	if err != nil {
		t.Fatalf("GetMigrationLeaseByToken: %v", err)
	}
	if got.Pending || got.MemStorageKey != "mem-key" || got.VMStateStorageKey != "vmstate-key" {
		t.Fatalf("completed lease = %+v, want completed handles", got)
	}

	if err := store.RenewMigrationLease(ctx, lease.LeaseToken, "wrong-node", now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("RenewMigrationLease(wrong node) = %v, want ErrConflict", err)
	}
	if err := store.RenewMigrationLease(ctx, lease.LeaseToken, lease.SourceNodeID, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("RenewMigrationLease: %v", err)
	}
	if err := store.ReserveMigrationLease(ctx, MigrationLease{
		InstanceID:     lease.InstanceID,
		LeaseToken:     "lease-token-live-retry",
		SourceNodeID:   lease.SourceNodeID,
		CreatedAt:      now,
		LeaseExpiresAt: now.Add(time.Hour),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("ReserveMigrationLease(live duplicate) = %v, want ErrConflict", err)
	}

	// Expire the lease through the normal renew path, then verify stale
	// renewal and replacement semantics.
	if err := store.RenewMigrationLease(ctx, lease.LeaseToken, lease.SourceNodeID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("RenewMigrationLease(expire): %v", err)
	}
	if err := store.RenewMigrationLease(ctx, lease.LeaseToken, lease.SourceNodeID, now.Add(time.Hour)); !errors.Is(err, ErrMigrationLeaseExpired) {
		t.Fatalf("RenewMigrationLease(expired) = %v, want ErrMigrationLeaseExpired", err)
	}
	replacement := lease
	replacement.LeaseToken = "lease-token-replacement"
	replacement.LeaseExpiresAt = now.Add(time.Hour)
	if err := store.ReserveMigrationLease(ctx, replacement); err != nil {
		t.Fatalf("ReserveMigrationLease(expired replacement): %v", err)
	}
	if _, err := store.GetMigrationLease(ctx, lease.InstanceID, lease.LeaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMigrationLease(replaced) = %v, want ErrNotFound", err)
	}

	if _, err := store.GetMigrationLease(ctx, "wrong-instance", replacement.LeaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMigrationLease(wrong instance) = %v, want ErrNotFound", err)
	}
	if _, err := store.GetMigrationLeaseByToken(ctx, "missing-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMigrationLeaseByToken(missing) = %v, want ErrNotFound", err)
	}
	if err := store.CompleteMigrationLease(ctx, "missing-token", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CompleteMigrationLease(missing) = %v, want ErrNotFound", err)
	}
	if err := store.RenewMigrationLease(ctx, "missing-token", "node-a", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RenewMigrationLease(missing) = %v, want ErrNotFound", err)
	}

	expired := replacement
	expired.InstanceID = "instance-lease-2"
	expired.LeaseToken = "lease-token-expired"
	expired.CreatedAt = now.Add(-2 * time.Minute)
	expired.LeaseExpiresAt = now.Add(-time.Minute)
	if err := store.ReserveMigrationLease(ctx, expired); err != nil {
		t.Fatalf("ReserveMigrationLease(expired row): %v", err)
	}
	rows, err := store.ListExpiredMigrationLeases(ctx, now)
	if err != nil {
		t.Fatalf("ListExpiredMigrationLeases: %v", err)
	}
	if len(rows) != 1 || rows[0].LeaseToken != expired.LeaseToken {
		t.Fatalf("expired leases = %+v, want token %q", rows, expired.LeaseToken)
	}
	if err := store.DeleteMigrationLease(ctx, expired.LeaseToken); err != nil {
		t.Fatalf("DeleteMigrationLease: %v", err)
	}
	if err := store.DeleteMigrationLease(ctx, expired.LeaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteMigrationLease(missing) = %v, want ErrNotFound", err)
	}
}

func TestMemStoreJobQuotaAdmissionAndUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()

	job, err := store.JobCreateIfUnderQuota(ctx, "account-job", "job-one", "app", "image", []string{"run"}, 128, 30, 1, 1, nil, 1)
	if err != nil {
		t.Fatalf("JobCreateIfUnderQuota: %v", err)
	}
	if string(job.EnvOverrides) != "{}" {
		t.Fatalf("empty env overrides = %s, want {}", job.EnvOverrides)
	}
	_, err = store.JobCreateIfUnderQuota(ctx, "account-job", "job-two", "app", "image", nil, 128, 30, 1, 1, json.RawMessage(`{}`), 1)
	if !errors.Is(err, ErrJobQuotaExceeded) {
		t.Fatalf("JobCreateIfUnderQuota(at quota) = %v, want ErrJobQuotaExceeded", err)
	}
	var quotaErr *JobQuotaError
	if !errors.As(err, &quotaErr) || quotaErr.Scope != JobQuotaScopePerAccount || quotaErr.Limit != 1 || quotaErr.Observed != 1 {
		t.Fatalf("quota error = %v, want per-account limit=1 observed=1", err)
	}

	deleted := "deleted"
	if _, err := store.JobUpdate(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, &deleted); err != nil {
		t.Fatalf("JobUpdate(delete): %v", err)
	}
	if _, err := store.JobCreateIfUnderQuota(ctx, "account-job", "job-two", "app", "image", nil, 128, 30, 1, 1, nil, 1); err != nil {
		t.Fatalf("JobCreateIfUnderQuota(after delete): %v", err)
	}

	minute := time.Now().UTC().Truncate(time.Minute)
	if err := store.AppendJobUsage(ctx, "account-job", "", "instance-job", minute, 1, 1, 1, 1, 1, 1, 1, 1); err == nil {
		t.Fatal("AppendJobUsage(empty job id) succeeded, want validation error")
	}
	if err := store.AppendJobUsage(ctx, "account-job", "job-one", "instance-job", minute, 100, 2, 3, 4, 5, 6, 1, 7); err != nil {
		t.Fatalf("AppendJobUsage: %v", err)
	}
	if err := store.AppendJobUsage(ctx, "account-job", "job-one", "instance-job", minute.Add(30*time.Second), 999, 999, 10, 11, 12, 13, 2, 8); err != nil {
		t.Fatalf("AppendJobUsage(redelivery): %v", err)
	}
	if len(store.usage) != 1 {
		t.Fatalf("usage rows = %d, want 1", len(store.usage))
	}
	row := store.usage[0]
	if row.MeterKind != "job" || row.JobID != "job-one" || row.AppID != "job-one" || row.MBSeconds != 100 || row.Requests != 2 {
		t.Fatalf("job usage identity/billing = %+v", row)
	}
	if row.CPUUsec != 13 || row.TXBytes != 15 || row.NetTxBytes != 17 || row.NetRxBytes != 19 || row.ColdBootCount != 3 || row.TailSeconds != 15 {
		t.Fatalf("job usage additive counters = %+v", row)
	}
}
