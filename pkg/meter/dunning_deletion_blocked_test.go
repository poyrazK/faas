package meter_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type countingDeletionCommands struct{ calls int }

func (f *countingDeletionCommands) Schedule(context.Context, string, time.Time) (api.InternalMailNonpaymentDeletionResponse, error) {
	f.calls++
	return api.InternalMailNonpaymentDeletionResponse{}, nil
}

func (f *countingDeletionCommands) Complete(context.Context, string, string, string) error { return nil }

// Billing review 2026-11-13 (R9-1, ADR-20261114093000): an account the
// retired ladder suspended for non-payment, past the old 21-day deletion
// deadline and owning managed PostgreSQL databases, moves to Free on Postgres
// too. It is never scheduled for deletion, whether the durable deletion
// command is on or off, its databases are untouched, and the deletion-blocked
// gauge stays published at zero.
// adr: 20261030120000
func TestDunningNonpaymentMovesToFreeAndNeverDeletes(t *testing.T) {
	for _, commandOn := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "durable command"}[commandOn], func(t *testing.T) {
			ctx := t.Context()
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			acct, err := store.CreateAccount(ctx, strings.ReplaceAll(uuid.NewString(), "-", "")+"@example.com", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE accounts SET status='suspended',past_due_at=clock_timestamp()-interval '22 days' WHERE id=$1`, acct.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint)
VALUES($1,$2,'orders','eu',17,'development','single_zone','test',$3)`, uuid.NewString(), acct.ID, strings.Repeat("a", 64)); err != nil {
				t.Fatal(err)
			}
			commands := &countingDeletionCommands{}
			d := meter.NewDunning(meter.DunningParams{
				Store: store, Parker: &fakeParker{}, Notif: &fakeNotifier{}, Mailer: &recordingSender{}, Log: discardLog(),
				NonpaymentDeletionRequested: func() bool { return commandOn },
				NonpaymentDeletionCommands:  commands,
				Registerer:                  prometheus.NewRegistry(),
			})
			if err := d.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := store.AccountByID(ctx, acct.ID)
			if err != nil || got.Status != state.AccountActive || got.Plan != api.PlanFree || got.PastDueAt == nil {
				t.Fatalf("account = status %s plan %s past_due_at %v (%v); want active on Free with the balance marked", got.Status, got.Plan, got.PastDueAt, err)
			}
			var live int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_postgres_databases WHERE account_id=$1 AND deleted_at IS NULL`, acct.ID).Scan(&live); err != nil || live != 1 {
				t.Fatalf("live databases = %d (%v), want 1", live, err)
			}
			if commands.calls != 0 {
				t.Fatalf("deletion command called %d time(s)", commands.calls)
			}
			for _, reason := range []string{api.NonpaymentDeletionBlockedManagedPostgres, api.NonpaymentDeletionBlockedSharedAppOwnership, "unknown"} {
				if v := testutil.ToFloat64(d.BlockedGauge().WithLabelValues(reason)); v != 0 {
					t.Fatalf("blocked{%s} = %v, want 0", reason, v)
				}
			}
		})
	}
}
