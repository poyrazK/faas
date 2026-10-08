//go:build !no_pg

package globaldirectory

// adr: 775
import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestQuotaOutboxPostgresContract(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 1)
	exerciseQuotaOutbox(t, f)
}

func TestQuotaOutboxPostgresSigningExpiry(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 1)
	exerciseQuotaOutboxSigningExpiry(t, f)
}

func TestQuotaOutboxPostgresLeaseExpiryAfterRowWait(t *testing.T) {
	pool, f := postgresQuotaFixture(t, false, 1)
	q, req := outboxFixture(t, f)
	if _, err := q.QueueDelivery(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	rows, err := (&sqlc.Queries{}).ClaimGlobalQuotaDeliveries(t.Context(), pool, sqlc.ClaimGlobalQuotaDeliveriesParams{Batch: 1, LeaseSeconds: 1})
	if err != nil || len(rows) != 1 {
		t.Fatal("short claim", err)
	}
	claim, err := quotaDeliveryRow(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	tx := beginDirectoryTx(t, pool)
	if _, err = tx.Exec(t.Context(), "SELECT id FROM global_runtime_quota_deliveries WHERE id=$1 FOR UPDATE", req.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- f.store.(QuotaDeliveryStore).FinishQuotaDelivery(ctx, claim, QuotaDeliveryPending, "uncertain", nil)
	}()
	blocked := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%finish_global_quota_delivery%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("finish never waited on delivery row")
	}
	time.Sleep(max(0, time.Until(*claim.ClaimUntil)) + 20*time.Millisecond)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrQuotaDeliveryClaim) {
		t.Fatal("expired finish survived lock wait", err)
	}
	restarted := NewPgStore(pool)
	claims, err := restarted.ClaimQuotaDeliveries(ctx)
	if err != nil || len(claims) != 1 || claims[0].ClaimID == claim.ClaimID || claims[0].Attempts != 2 {
		t.Fatal("restart did not reclaim", err)
	}
	if err = restarted.FinishQuotaDelivery(ctx, claim, QuotaDeliveryPending, "backend", nil); !errors.Is(err, ErrQuotaDeliveryClaim) {
		t.Fatal("stale worker finished replacement", err)
	}
}

func TestQuotaOutboxPostgresRetentionAndSQLGuards(t *testing.T) {
	pool, f := postgresQuotaFixture(t, false, 1)
	q, req := outboxFixture(t, f)
	if _, err := q.QueueDelivery(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := f.quota.RetireRuntimeQuota(t.Context(), req.OwnerID, f.actor.ID, req.Slug, req.AllocationID); err != nil {
		t.Fatal(err)
	}
	req.ID, req.Purpose, req.ConsentID = "b97c9424-9212-45c7-ace1-201b8260d217", QuotaDeliveryRetirement, ""
	retirement, err := q.QueueDelivery(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"UPDATE global_runtime_quota_deliveries SET issuer_id=gen_random_uuid() WHERE id=$1",
		"UPDATE global_runtime_quota_deliveries SET envelope=jsonb_set(envelope,'{payload}','\"changed\"') WHERE id=$1",
		"UPDATE global_runtime_quota_deliveries SET attempts=attempts+1 WHERE id=$1",
		"UPDATE global_runtime_quota_deliveries SET status='accepted',accepted_at=now(),receipt='{}' WHERE id=$1",
		"DELETE FROM global_runtime_quota_deliveries WHERE id=$1",
	} {
		if _, err := pool.Exec(t.Context(), stmt, req.ID); err == nil {
			t.Fatal("SQL bypass", stmt)
		}
	}
	for _, stmt := range []string{"TRUNCATE global_runtime_quota_deliveries", "TRUNCATE global_runtime_quota_delivery_owners", "DELETE FROM global_runtime_quota_delivery_owners"} {
		if _, err := pool.Exec(t.Context(), stmt); err == nil {
			t.Fatal("retention bypass", stmt)
		}
	}
	if _, err = pool.Exec(t.Context(), "DELETE FROM global_apps WHERE id=$1", retirement.GlobalAppID); err == nil {
		t.Fatal("collection bypassed retained runtime quota")
	}
	if err = f.store.DetachTarget(t.Context(), req.OwnerID, req.Slug, req.Region); err != nil {
		t.Fatal(err)
	}
	if err = f.authority.UpdateAccountStatus(t.Context(), f.actor.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	restarted := NewPgStore(pool)
	if got, err := restarted.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); err != nil || got.Delivery != retirement.Delivery {
		t.Fatal("retained original lost", err)
	}
	claims, err := restarted.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(claims) != 1 {
		t.Fatal("collected claim", err)
	}
	if claims[0].Purpose == QuotaDeliveryGrant {
		if err = restarted.CheckQuotaDeliveryDispatch(t.Context(), claims[0]); err == nil {
			t.Fatal("collected grant dispatched")
		}
		if err = restarted.FinishQuotaDelivery(t.Context(), claims[0], QuotaDeliveryBlocked, "binding", nil); err != nil {
			t.Fatal(err)
		}
		claims, err = restarted.ClaimQuotaDeliveries(t.Context())
		if err != nil || len(claims) != 1 {
			t.Fatal(err)
		}
	}
	if claims[0].Purpose != QuotaDeliveryRetirement || restarted.CheckQuotaDeliveryDispatch(t.Context(), claims[0]) != nil {
		t.Fatal("collection blocked signed narrowing")
	}
	m, err := os.ReadFile("../../migrations/20261006204529632_global_runtime_quota_delivery_outbox.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(m), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration parts")
	}
	if _, err = pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("retained outbox downgraded")
	}
}
