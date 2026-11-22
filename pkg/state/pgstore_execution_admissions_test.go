//go:build !no_pg

package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgExecutionAdmissionConcurrentReplay(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "keyed-concurrent")
	exerciseExecutionAdmission(t, store, account.ID)
}

func TestPgExecutionAdmissionGrantAndTerminalReplay(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "grant-replay")
	exerciseExecutionAdmissionGrantReplay(t, store, account.ID)
}

func TestPgExecutionAdmissionFailureRollback(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "keyed-failures")
	exerciseExecutionAdmissionFailures(t, store, account.ID)
}

func TestPgExecutionAdmissionReaderAndRestartWithOneConnection(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "keyed-reader")
	params := pgExecutionParams(t, account.ID, time.Now().UTC(), 0, "sealed")
	source, err := store.CreateExecution(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	config := pool.Config()
	config.MaxConns = 1
	single, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	identity := admissionIdentity(account.ID, "restart")
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	first, replayed, err := state.NewPgStore(single).AdmitExecution(ctx, identity, func(ctx context.Context, reader state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		if _, err := reader.ExecutionByID(ctx, account.ID, source.ID); err != nil {
			return state.CreateExecutionParams{}, err
		}
		if _, err := reader.ExecutionArtifactGrantByToken(ctx, account.ID, make([]byte, 32), time.Now()); !errors.Is(err, state.ErrNotFound) {
			return state.CreateExecutionParams{}, errors.New("unexpected grant lookup")
		}
		return params, nil
	})
	if err != nil || replayed {
		t.Fatalf("single-connection admission: %v, %v", replayed, err)
	}
	// A fresh store/pool has no response cache in memory to rely on.
	restarted, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	second, replayed, err := state.NewPgStore(restarted).AdmitExecution(ctx, identity, func(context.Context, state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		t.Error("restart replay prepared another Run")
		return state.CreateExecutionParams{}, errors.New("must not prepare")
	})
	if err != nil || !replayed || second.ID != first.ID {
		t.Fatalf("restart replay: %s, %v, %v", second.ID, replayed, err)
	}
}

func TestPgExecutionAdmissionLedgerFailureRollsBackReceiptPayloadAndEvents(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account := pgExecutionAccount(t, store, ctx, "keyed-ledger-rollback")
	grant, tokenHash, base := prepareExecutionAdmissionGrant(t, store, account.ID)
	counts := func() [4]int {
		t.Helper()
		var result [4]int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM executions), (SELECT count(*) FROM execution_payloads), (SELECT count(*) FROM execution_events), (SELECT count(*) FROM execution_admissions)`).Scan(&result[0], &result[1], &result[2], &result[3]); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := counts()
	// Inject failure at the last durable write, after the receipt, payload and
	// initial event have been inserted, rather than only failing preparation.
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_run_admission() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected ledger failure'; END $$;
CREATE TRIGGER reject_run_admission BEFORE INSERT ON execution_admissions FOR EACH ROW EXECUTE FUNCTION reject_run_admission()`)
	if err != nil {
		t.Fatal(err)
	}
	identity := admissionIdentity(account.ID, uuid.NewString())
	identity.KeyHash = sha256.Sum256([]byte("ledger-failure"))
	params := pgExecutionParams(t, account.ID, base.Add(5*time.Millisecond), 0, "sealed")
	prepare := func(ctx context.Context, reader state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		loaded, err := reader.ExecutionArtifactGrantByToken(ctx, account.ID, tokenHash[:], params.AdmittedAt)
		if err != nil {
			return state.CreateExecutionParams{}, err
		}
		params.ArtifactGrantRedemptions = []state.ExecutionArtifactGrantRedemption{{GrantID: loaded.ID, TokenHash: loaded.TokenHash}}
		return params, nil
	}
	if _, _, err := store.AdmitExecution(ctx, identity, prepare); err == nil {
		t.Fatal("ledger failure admitted Run")
	}
	if after := counts(); before != after {
		t.Fatalf("partial admission: before=%v after=%v", before, after)
	}
	loaded, err := store.ExecutionArtifactGrantByToken(ctx, account.ID, tokenHash[:], params.AdmittedAt)
	if err != nil || loaded.ID != grant.ID {
		t.Fatalf("rollback consumed grant: %s, %v", loaded.ID, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_run_admission ON execution_admissions`); err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := store.AdmitExecution(ctx, identity, prepare); err != nil || replayed {
		t.Fatalf("retry after rollback: %v, %v", replayed, err)
	}
}
