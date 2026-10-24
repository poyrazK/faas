package meter_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
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

type blockedCommandFake struct{ err error }

func (f *blockedCommandFake) Schedule(context.Context, string, time.Time) (api.InternalMailNonpaymentDeletionResponse, error) {
	return api.InternalMailNonpaymentDeletionResponse{}, f.err
}

func (f *blockedCommandFake) Complete(context.Context, string, string, string) error { return nil }

// A suspended account past the deletion deadline that a deletion guard holds
// stays suspended, is logged and counted by guard, and is not reported as an
// outage. The gauge clears on the first sweep after the block is gone.
func TestDunningNonpaymentDeletionBlockedIsCounted(t *testing.T) {
	tests := []struct {
		name      string
		databases int   // live personal databases seeded for the direct store path
		command   error // nil: the direct store path (durable command off)
		reason    string
		log       string
	}{
		{name: "store guard", databases: 2, reason: api.NonpaymentDeletionBlockedManagedPostgres, log: "non-payment deletion blocked"},
		{name: "command managed postgres", command: &api.InternalMailNonpaymentDeletionBlocked{Reason: api.NonpaymentDeletionBlockedManagedPostgres, ManagedPostgresDatabases: 2}, reason: api.NonpaymentDeletionBlockedManagedPostgres, log: "non-payment deletion blocked"},
		{name: "command shared ownership", command: &api.InternalMailNonpaymentDeletionBlocked{Reason: api.NonpaymentDeletionBlockedSharedAppOwnership}, reason: api.NonpaymentDeletionBlockedSharedAppOwnership, log: "non-payment deletion blocked"},
		{name: "command reason from a newer apid", command: &api.InternalMailNonpaymentDeletionBlocked{Reason: "retention_hold"}, reason: "unknown", log: "non-payment deletion blocked"},
		{name: "command outage", command: errors.New("non-payment deletion command returned HTTP 503"), log: "durable non-payment deletion unavailable"},
	}
	reasons := []string{api.NonpaymentDeletionBlockedManagedPostgres, api.NonpaymentDeletionBlockedSharedAppOwnership, "unknown"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			for i := range tt.databases {
				if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint)
VALUES($1,$2,$3,'eu',17,'development','single_zone','test',$4)`, uuid.NewString(), acct.ID, fmt.Sprintf("orders-%d", i), strings.Repeat("a", 64)); err != nil {
					t.Fatal(err)
				}
			}
			fake := &blockedCommandFake{err: tt.command}
			var logs bytes.Buffer
			d := meter.NewDunning(meter.DunningParams{
				Store: store, Parker: &fakeParker{}, Notif: &fakeNotifier{}, Mailer: &recordingSender{},
				Log:                         slog.New(slog.NewJSONHandler(&logs, nil)),
				NonpaymentDeletionRequested: func() bool { return tt.command != nil },
				NonpaymentDeletionCommands:  fake,
				Registerer:                  prometheus.NewRegistry(),
			})
			if err := d.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := store.AccountByID(ctx, acct.ID)
			if err != nil || got.Status != state.AccountSuspended {
				t.Fatalf("blocked account left suspended: %v %v", got.Status, err)
			}
			for _, reason := range reasons {
				want := 0.0
				if reason == tt.reason {
					want = 1
				}
				if v := testutil.ToFloat64(d.BlockedGauge().WithLabelValues(reason)); v != want {
					t.Fatalf("blocked{%s} = %v, want %v", reason, v, want)
				}
			}
			if !strings.Contains(logs.String(), tt.log) {
				t.Fatalf("log lacks %q: %s", tt.log, logs.String())
			}

			// Clear the block: the databases are deleted, or apid answers.
			if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleted',deleted_at=now() WHERE account_id=$1`, acct.ID); err != nil {
				t.Fatal(err)
			}
			fake.err = nil
			if err := d.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			for _, reason := range reasons {
				if v := testutil.ToFloat64(d.BlockedGauge().WithLabelValues(reason)); v != 0 {
					t.Fatalf("blocked{%s} = %v after the block cleared", reason, v)
				}
			}
			if tt.command == nil {
				if got, err := store.AccountByID(ctx, acct.ID); err != nil || got.Status != state.AccountDeletedPending {
					t.Fatalf("store path did not advance once unblocked: %v %v", got.Status, err)
				}
			}
		})
	}
}
