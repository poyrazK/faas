//go:build !no_pg

package state

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgApplicationStandardLogDeliveryConcurrentProjection(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	ins := standardLogDeliveryInstance(t, s, f)
	d := standardLogDeliveryDrain(t, s, f)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE app_application_standards SET desired_revision=desired_revision+1,state='pending' WHERE app_id=$1`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 1)
	var locked *pgconn.PgError
	if !errors.As(err, &locked) || locked.Code != "55P03" {
		t.Fatalf("receipt waited on intent writer: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 1); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("committed newer intent accepted old receipt: %v", err)
	}
	rows, err := s.ListApplicationStandardLogDeliveries(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal("concurrent projection left an accepted receipt")
	}
}

func TestPgApplicationStandardLogDeliveryRawGuardAndClock(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	ins := standardLogDeliveryInstance(t, s, f)
	d := standardLogDeliveryDrain(t, s, f)
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_log_deliveries SET effective_hash=repeat('f',64) WHERE app_id=$1`, f.app.ID); !errors.Is(standardLogDeliveryPGError(err), ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("raw stale tuple accepted: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE application_standard_log_deliveries SET observed_at=$2 WHERE app_id=$1`, f.app.ID, future); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListApplicationStandardLogDeliveries(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != 1 || rows[0].ObservedAt.After(time.Now()) {
		t.Fatal("caller supplied observation clock")
	}
	if _, err := s.ScheduleAppDeletion(ctx, f.app.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_standard_log_deliveries WHERE app_id=$1`, f.app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("parent cascade retained receipt")
	}
}

func TestPgApplicationStandardLogDelivery(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardLogDeliveryLifecycle(t, s)
}

func TestPgApplicationStandardLogDeliveryExceptionDeadline(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardLogDeliveryExceptionDeadline(t, s)
}

func TestPgApplicationStandardLogDeliveryParentAuthority(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardLogDeliveryParentAuthority(t, s)
}
