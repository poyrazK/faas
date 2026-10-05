//go:build !no_pg

package state

// adr: 631
import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGRegionalRuntimeRetirementContract(t *testing.T) {
	runRegionalRuntimeRetirementContract(t, pgRegionalCapacityFixture)
}

func TestPGRegionalRuntimeRetirementFirstFenceLockWait(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			f := pgRegionalCapacityFixture(t)
			s := f.ledger.(*PgStore)
			p := f.permit(runtimequota.CapacityWarm)
			f.reserve(t, p)
			f.issue(t, p)
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
			first, proposed := uuid.NewString(), uuid.NewString()
			if _, err = q.FenceRegionalRuntimeCapacity(t.Context(), tx, sqlc.FenceRegionalRuntimeCapacityParams{AppID: uuidToPgtype(f.app()), PermitID: uuidToPgtype(p.ID), FenceID: uuidToPgtype(first)}); err != nil {
				t.Fatal(err)
			}
			type result struct {
				r   RegionalRuntimeCapacityReceipt
				err error
			}
			done := make(chan result, 1)
			go func() { r, err := s.BeginRegionalRuntimeRetirement(t.Context(), p, proposed); done <- result{r, err} }()
			waitQuotaWriteBlocked(t, s.pool, pid, "LockRegionalRuntimeQuotaJournal")
			if commit {
				err = tx.Commit(t.Context())
			} else {
				err = tx.Rollback(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			got := <-done
			want := proposed
			if commit {
				want = first
			}
			if got.err != nil || got.r.FenceID != want || got.r.ReleasedAt != nil {
				t.Fatal("first fence did not follow committed state", got)
			}
			stop := runtimequota.CapacityStop{Permit: p, FenceID: want, AdmissionFrozen: true}
			if _, err = f.authority.CompleteHostRetirement(t.Context(), stop, f.cell); err != nil {
				t.Fatal(err)
			}
		})
	}
}
