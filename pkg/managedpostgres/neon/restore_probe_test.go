package neon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestRestoreDataProbeRejectsPostRestorePointMutation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	started := time.Now().UTC()
	probe, err := prepareRestoreProbe(ctx, conn.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if probe.PointInTime.Before(started) || probe.PointInTime.After(time.Now().UTC()) || probe.PointInTime.Nanosecond() != 0 {
		t.Fatalf("point = %v", probe.PointInTime)
	}
	if err := verifyRestoreProbe(ctx, conn.Conn(), probe); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("current data incorrectly verified: %v", err)
	}
	// Test the verifier's acceptance against the earlier marker. Actual Neon
	// PITR remains an explicit live qualification, never simulated evidence.
	if _, err := conn.Exec(ctx, `UPDATE public.gregale_qualification_restore_probe SET marker = $1 WHERE id = 1`, probe.Marker); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestoreProbe(ctx, conn.Conn(), probe); err != nil {
		t.Fatal(err)
	}
}
