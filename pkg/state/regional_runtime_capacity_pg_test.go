//go:build !no_pg

package state

// adr: 492
import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func regionalCapacityMigration(t *testing.T) (string, string) {
	t.Helper()
	b, err := migrations.FS.ReadFile("20261004220000001_regional_runtime_capacity_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		t.Fatal("missing capacity downgrade")
	}
	return up, down
}
func pgRegionalCapacityFixture(t *testing.T) regionalCapacityFixture {
	t.Helper()
	s, _, cell, account := releasePGFixture(t)
	return newRegionalCapacityFixture(t, s, s, account, cell)
}
func TestPGRegionalRuntimeCapacityContract(t *testing.T) {
	runRegionalRuntimeCapacityContract(t, pgRegionalCapacityFixture)
}

func TestPGRegionalRuntimeCapacityRestartCollection(t *testing.T) {
	f := pgRegionalCapacityFixture(t)
	s := f.ledger.(*PgStore)
	p := f.permit(runtimequota.CapacityWarm)
	f.reserve(t, p)
	f.issue(t, p)
	seat := f.seat(t, p, false)
	f.issueSeat(t, seat)
	original, err := s.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), "DELETE FROM apps WHERE id=$1", f.app()); err != nil {
		t.Fatal(err)
	}
	f.ledger = NewPgStore(s.pool)
	f.authority.Store = f.ledger
	recovered, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
	if err != nil || recovered.Permit != original.Permit || recovered.IssuedAt == nil || !recovered.CreatedAt.Equal(original.CreatedAt) || !recovered.IssuedAt.Equal(*original.IssuedAt) {
		t.Fatalf("restart: %+v %v", recovered, err)
	}
	f.usage(t, 1, 1, 1)
	retirement, e := f.quota.retirement(t, f.quota.grant)
	if _, err = f.quota.authority.AcceptRetirement(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	f.stop(t, p, retirement.RetirementID)
	f.usage(t, 0, 0, 0)
	replay, err := f.ledger.ReserveRegionalRuntimeCapacity(t.Context(), p)
	if err != nil || replay.ReleasedAt == nil || replay.Stop == nil {
		t.Fatalf("collected retry: %+v %v", replay, err)
	}
	replaySeat, err := f.ledger.ReserveRegionalRuntimeServingSeat(t.Context(), seat)
	if err != nil || !replaySeat.ClosedByPhysicalStop {
		t.Fatalf("collected seat retry: %+v %v", replaySeat, err)
	}
}

func TestPGRegionalRuntimeCapacitySQLGuards(t *testing.T) {
	f := pgRegionalCapacityFixture(t)
	s := f.ledger.(*PgStore)
	p := f.permit(runtimequota.CapacityServing)
	f.reserve(t, p)
	seat := f.seat(t, p, false)
	for _, statement := range []string{
		`UPDATE regional_runtime_capacity_receipts SET issued_at=now(),fence_id=gen_random_uuid()`,
		`UPDATE regional_runtime_capacity_receipts SET permit_id=gen_random_uuid()`,
		`UPDATE regional_runtime_capacity_receipts SET generation=generation+1`,
		`UPDATE regional_runtime_capacity_receipts SET node_id=gen_random_uuid()`,
		`UPDATE regional_runtime_capacity_receipts SET host_epoch_id=gen_random_uuid()`,
		`UPDATE regional_runtime_capacity_receipts SET created_at=created_at+interval '1 second'`,
		`UPDATE regional_runtime_capacity_receipts SET kind='warm'`,
		`UPDATE regional_runtime_serving_seats SET issued_at=now()`,
		`UPDATE regional_runtime_serving_seats SET rollout=true`,
		`UPDATE regional_runtime_serving_seats SET seat_id=gen_random_uuid()`,
		`UPDATE regional_runtime_serving_seats SET closed_by_physical_stop=true,released_at=now(),fence_id=gen_random_uuid()`,
		`DELETE FROM regional_runtime_serving_seats`,
		`DELETE FROM regional_runtime_capacity_receipts`,
		`TRUNCATE regional_runtime_serving_seats`,
		`TRUNCATE regional_runtime_capacity_receipts CASCADE`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("SQL bypass allowed %s", statement)
		}
	}
	f.issue(t, p)
	f.issueSeat(t, seat)
	for _, statement := range []string{
		`UPDATE regional_runtime_capacity_receipts SET issued_at=NULL`,
		`UPDATE regional_runtime_capacity_receipts SET released_at=now(),fence_id=gen_random_uuid()`,
		`UPDATE regional_runtime_serving_seats SET released_at=now(),fence_id=gen_random_uuid()`,
		`UPDATE regional_runtime_serving_seats SET issued_at=NULL`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("phase bypass allowed %s", statement)
		}
	}
	// A claimed zero must not combine a newly written fence with a stop assertion.
	fence := uuid.NewString()
	stop := runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true}
	claims, err := json.Marshal(stop)
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.cell.SignCapacityStop(stop)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE regional_runtime_capacity_receipts SET fence_id=$1,released_at=now(),stop_claims=$2,stop_envelope=$3`, fence, claims, envelope); err == nil {
		t.Fatal("fence/stop phase jump accepted")
	}
	if _, err = s.FenceRegionalRuntimeCapacity(t.Context(), f.app(), p.ID, fence); err != nil {
		t.Fatal(err)
	}
	f.stop(t, p, fence)
	for _, statement := range []string{
		`UPDATE regional_runtime_capacity_receipts SET fence_id=gen_random_uuid()`,
		`UPDATE regional_runtime_capacity_receipts SET stop_envelope='{}'`,
		`UPDATE regional_runtime_capacity_receipts SET released_at=NULL,stop_claims=NULL,stop_envelope=NULL`,
		`UPDATE regional_runtime_serving_seats SET closed_by_physical_stop=false`,
		`UPDATE regional_runtime_serving_seats SET released_at=NULL`,
	} {
		if _, err = s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("retained proof bypass allowed %s", statement)
		}
	}
	_, down := regionalCapacityMigration(t)
	if _, err = s.pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("released receipt downgrade: %v", err)
	}
	f.usage(t, 0, 0, 0)
}

func TestPGRegionalRuntimeCapacityEmptyMigrationRoundTrip(t *testing.T) {
	s, _, _, _ := releasePGFixture(t)
	up, down := regionalCapacityMigration(t)
	for _, statement := range []string{down, up} {
		if _, err := s.pool.Exec(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPGRegionalRuntimeCapacityFixedSnapshot(t *testing.T) {
	f := pgRegionalCapacityFixture(t)
	s := f.ledger.(*PgStore)
	p := f.permit(runtimequota.CapacityServing)
	f.reserve(t, p)
	seat := f.seat(t, p, false)
	for _, level := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		for _, statement := range []string{`UPDATE regional_runtime_capacity_receipts SET issued_at=now()`, `UPDATE regional_runtime_serving_seats SET issued_at=now()`} {
			tx, err := s.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: level})
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(t.Context(), statement)
			if !errors.Is(regionalCapacityError(err), ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("fixed snapshot admitted: %v", err)
			}
			_ = tx.Rollback(t.Context())
		}
	}
	if _, err := s.IssueRegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("fixed snapshot changed phase: %v", err)
	}
}

func TestPGRegionalRuntimeCapacityRetirementLockWait(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			f := pgRegionalCapacityFixture(t)
			s := f.ledger.(*PgStore)
			p := f.permit(runtimequota.CapacityWarm)
			f.reserve(t, p)
			tx, err := s.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			var pid int
			if err = tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			q := &sqlc.Queries{}
			if err = q.LockRegionalRuntimeQuotaJournal(t.Context(), tx, uuidToPgtype(f.app())); err != nil {
				t.Fatal(err)
			}
			retirement, e := f.quota.retirement(t, f.quota.grant)
			r, err := readRegionalQuota(t.Context(), tx, f.app(), f.quota.grant.AllocationID)
			if err != nil {
				t.Fatal(err)
			}
			r.Retirement = &retirement
			r.RetirementEnvelope = &e
			params, err := regionalQuotaParams(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = q.RetireRegionalRuntimeQuotaReceipt(t.Context(), tx, sqlc.RetireRegionalRuntimeQuotaReceiptParams{AppID: params.AppID, AllocationID: params.AllocationID, Retirement: params.Retirement, RetirementEnvelope: params.RetirementEnvelope}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, issueErr := s.IssueRegionalRuntimeCapacity(t.Context(), f.app(), p.ID); done <- issueErr }()
			waitQuotaWriteBlocked(t, s.pool, pid, "LockRegionalRuntimeQuotaJournal")
			if commit {
				err = tx.Commit(t.Context())
			} else {
				err = tx.Rollback(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			issueErr := <-done
			if commit && !errors.Is(issueErr, ErrConflict) {
				t.Fatalf("missed committed retirement: %v", issueErr)
			}
			if !commit && issueErr != nil {
				t.Fatalf("rollback froze permit: %v", issueErr)
			}
			rCapacity, err := s.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
			if err != nil || (rCapacity.ReleasedAt != nil) != commit || (rCapacity.IssuedAt == nil) != commit {
				t.Fatalf("lock wait phase: %+v %v", rCapacity, err)
			}
		})
	}
}
