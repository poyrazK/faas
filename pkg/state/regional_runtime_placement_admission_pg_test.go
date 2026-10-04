//go:build !no_pg

package state

// adr: 496
import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGRegionalRuntimeIssuedPlacementContract(t *testing.T) {
	runIssuedRegionalPlacementContract(t, pgRegionalCapacityFixture)
}

func TestPGRegionalRuntimeIssuedPlacementWaitsForFence(t *testing.T) {
	for _, target := range []string{"physical", "seat"} {
		for _, commit := range []bool{false, true} {
			t.Run(target+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				f := pgRegionalCapacityFixture(t)
				s := f.ledger.(*PgStore)
				placement := issuedPlacement(t, f, runtimequota.CapacityServing)
				tx, err := s.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(t.Context()) }()
				var holderPID int
				if err := tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
					t.Fatal(err)
				}
				q := &sqlc.Queries{}
				if err := q.LockRegionalRuntimeQuotaJournal(t.Context(), tx, uuidToPgtype(f.app())); err != nil {
					t.Fatal(err)
				}
				if target == "physical" {
					_, err = q.FenceRegionalRuntimeCapacity(t.Context(), tx, sqlc.FenceRegionalRuntimeCapacityParams{AppID: uuidToPgtype(f.app()), PermitID: uuidToPgtype(placement.Permit.ID), FenceID: uuidToPgtype(uuid.NewString())})
				} else {
					_, err = q.FenceRegionalRuntimeServingSeat(t.Context(), tx, sqlc.FenceRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(f.app()), SeatID: uuidToPgtype(placement.Seat.ID), FenceID: uuidToPgtype(uuid.NewString())})
				}
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- s.CheckRegionalRuntimeIssuedPlacement(t.Context(), placement) }()
				waitQuotaWriteBlocked(t, s.pool, holderPID, "LockRegionalRuntimeQuotaJournal")
				if commit {
					err = tx.Commit(t.Context())
				} else {
					err = tx.Rollback(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				checkErr := <-done
				if commit && !errors.Is(checkErr, ErrRegionalRuntimePlacementUnavailable) {
					t.Fatalf("check missed committed %s fence: %v", target, checkErr)
				}
				if !commit && checkErr != nil {
					t.Fatalf("rolled back %s fence blocked issued placement: %v", target, checkErr)
				}
				f.usage(t, 1, 1, 1)
			})
		}
	}
}
