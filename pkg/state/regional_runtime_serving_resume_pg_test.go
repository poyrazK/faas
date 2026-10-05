//go:build !no_pg

package state

// adr: 626
import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func regionalServingResumeMigration(t *testing.T) (string, string) {
	t.Helper()
	b, err := migrations.FS.ReadFile("20261005030000001_regional_runtime_serving_resumes.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		t.Fatal("missing serving resume downgrade")
	}
	return up, down
}

func TestPGRegionalServingResumeContract(t *testing.T) {
	runRegionalServingResumeContract(t, pgRegionalCapacityFixture)
}

func TestPGRegionalServingResumeRestartCollectionAndSQLGuards(t *testing.T) {
	f := pgRegionalCapacityFixture(t)
	s := f.ledger.(*PgStore)
	p := f.permit("warm")
	f.reserve(t, p)
	f.issue(t, p)
	original, err := s.IssueRegionalRuntimeServingResume(t.Context(), resumeIntent(t, p))
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewPgStore(s.pool)
	recovered, err := restarted.IssueRegionalRuntimeServingResume(t.Context(), resumeIntent(t, p))
	if err != nil || recovered.Intent != original.Intent || !recovered.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal(recovered, err)
	}
	for _, statement := range []string{
		`UPDATE regional_runtime_serving_resumes SET resume_id=gen_random_uuid()`,
		`UPDATE regional_runtime_serving_resumes SET permit_id=gen_random_uuid()`,
		`UPDATE regional_runtime_serving_resumes SET seat_id=gen_random_uuid()`,
		`UPDATE regional_runtime_serving_resumes SET intent_claims='{}'`,
		`UPDATE regional_runtime_serving_resumes SET created_at=created_at+interval '1 second'`,
		`DELETE FROM regional_runtime_serving_resumes`,
		`TRUNCATE regional_runtime_serving_resumes`,
	} {
		if _, err = s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatal("SQL bypass accepted", statement)
		}
	}
	_, down := regionalServingResumeMigration(t)
	if _, err = s.pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatal("nonempty downgrade", err)
	}
	for _, level := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		tx, beginErr := s.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: level})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		i := original.Intent
		i.ID = uuid.NewString()
		raw, marshalErr := json.Marshal(i)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		_, insertErr := (&sqlc.Queries{}).InsertRegionalRuntimeServingResume(t.Context(), tx, sqlc.InsertRegionalRuntimeServingResumeParams{AppID: uuidToPgtype(f.app()), ResumeID: uuidToPgtype(i.ID), PermitID: uuidToPgtype(p.ID), SeatID: uuidToPgtype(i.Seat.ID), IntentClaims: raw})
		_ = tx.Rollback(t.Context())
		if !errors.Is(regionalCapacityError(insertErr), ErrRegionalRuntimeQuotaFrozen) {
			t.Fatal("fixed snapshot admitted", insertErr)
		}
	}
	if _, err = s.pool.Exec(t.Context(), "DELETE FROM apps WHERE id=$1", f.app()); err != nil {
		t.Fatal(err)
	}
	recovered, err = restarted.RegionalRuntimeServingResume(t.Context(), f.app(), original.Intent.ID)
	if err != nil || recovered.Intent != original.Intent || !recovered.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal("collection lost intent", recovered, err)
	}
	f.usage(t, 1, 1, 1)
}

func TestPGRegionalServingResumeWaitsForFence(t *testing.T) {
	for _, target := range []string{"physical", "seat", "retirement"} {
		for _, commit := range []bool{false, true} {
			t.Run(target+map[bool]string{false: "-rollback", true: "-commit"}[commit], func(t *testing.T) {
				f := pgRegionalCapacityFixture(t)
				s := f.ledger.(*PgStore)
				p := f.permit("warm")
				f.reserve(t, p)
				f.issue(t, p)
				original, err := s.IssueRegionalRuntimeServingResume(t.Context(), resumeIntent(t, p))
				if err != nil {
					t.Fatal(err)
				}
				tx, err := s.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(t.Context()) }()
				q := &sqlc.Queries{}
				var pid int
				if err = tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				if err = q.LockRegionalRuntimeQuotaJournal(t.Context(), tx, uuidToPgtype(f.app())); err != nil {
					t.Fatal(err)
				}
				switch target {
				case "physical":
					_, err = q.FenceRegionalRuntimeCapacity(t.Context(), tx, sqlc.FenceRegionalRuntimeCapacityParams{AppID: uuidToPgtype(f.app()), PermitID: uuidToPgtype(p.ID), FenceID: uuidToPgtype(uuid.NewString())})
				case "seat":
					_, err = q.FenceRegionalRuntimeServingSeat(t.Context(), tx, sqlc.FenceRegionalRuntimeServingSeatParams{AppID: uuidToPgtype(f.app()), SeatID: uuidToPgtype(original.Intent.Seat.ID), FenceID: uuidToPgtype(uuid.NewString())})
				case "retirement":
					retirement, e := f.quota.retirement(t, f.quota.grant)
					r, readErr := readRegionalQuota(t.Context(), tx, f.app(), f.quota.grant.AllocationID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					r.Retirement, r.RetirementEnvelope = &retirement, &e
					params, paramsErr := regionalQuotaParams(r)
					if paramsErr != nil {
						t.Fatal(paramsErr)
					}
					_, err = q.RetireRegionalRuntimeQuotaReceipt(t.Context(), tx, sqlc.RetireRegionalRuntimeQuotaReceiptParams{AppID: params.AppID, AllocationID: params.AllocationID, Retirement: params.Retirement, RetirementEnvelope: params.RetirementEnvelope})
				}
				if err != nil {
					t.Fatal(err)
				}
				candidate := resumeIntent(t, p)
				done := make(chan error, 1)
				go func() { _, issueErr := s.IssueRegionalRuntimeServingResume(t.Context(), candidate); done <- issueErr }()
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
				if commit && issueErr == nil {
					t.Fatal("missed committed fence")
				}
				if !commit && issueErr != nil {
					t.Fatal("rollback retained fence", issueErr)
				}
				f.usage(t, 1, 1, 1)
			})
		}
	}
}

func TestPGRegionalServingResumeEmptyMigrationRoundTrip(t *testing.T) {
	s, _, _, _ := releasePGFixture(t)
	up, down := regionalServingResumeMigration(t)
	for _, statement := range []string{down, up} {
		if _, err := s.pool.Exec(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}
