//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

// On production-us (2026-11-03) exclusive_work_operations_app_id_fkey failed
// every purge of an app that had run exclusive work: the purge never deleted
// the operations. A finished operation is now purged with the app; a live
// claim makes the purge wait (exclusive_work).
func TestPgDeleteAppPermanentlyExclusiveWork(t *testing.T) {
	tests := []struct {
		name      string
		liveClaim bool
		want      error
	}{
		{name: "finished operation is purged with the app"},
		{name: "live claim waits", liveClaim: true, want: state.ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, pool, ctx := pgStoreWithPool(t)
			account, err := st.CreateAccount(ctx, uuid.NewString()+"@purge-exclusive.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "purge-ex-" + uuid.NewString()[:8],
				Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			policy := exclusivework.Policy{Name: "purge-lane", Scope: "account", MemberAppIDs: []string{app.ID},
				Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60, MaxAttempts: 2, RetryAfterSeconds: 2}
			if _, err := st.UpsertExclusiveWorkPolicy(ctx, account.ID, policy); err != nil {
				t.Fatal(err)
			}
			key, _ := json.Marshal("lane-1")
			op, _, err := st.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{AccountID: account.ID, AppID: app.ID,
				PolicyName: policy.Name, Key: key, Request: json.RawMessage(`{"kind":"sync"}`), IdempotencyKey: "purge-1"})
			if err != nil {
				t.Fatal(err)
			}
			if tt.liveClaim {
				if _, err := pool.Exec(ctx, `UPDATE exclusive_work_operations SET state='running', generation=1,
					claim_token=gen_random_uuid(), incarnation_id='purge-test', lease_expires_at=now()+interval '1 hour',
					attempt_deadline=now()+interval '1 hour' WHERE id=$1`, op.ID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := pool.Exec(ctx, `UPDATE exclusive_work_operations SET state='completed' WHERE id=$1`, op.ID); err != nil {
				t.Fatal(err)
			}
			expireApp(t, ctx, st, app.ID)

			err = st.DeleteAppPermanently(ctx, app.ID)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("DeleteAppPermanently = %v, want %v", err, tt.want)
			}
			if tt.liveClaim {
				var blocked *state.AppPurgeBlockedError
				if !errors.As(err, &blocked) || blocked.Reason != state.AppPurgeBlockedExclusiveWork {
					t.Fatalf("refusal = %v, want reason %s", err, state.AppPurgeBlockedExclusiveWork)
				}
			}
			var apps, ops int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM apps WHERE id=$1), (SELECT count(*) FROM exclusive_work_operations WHERE app_id=$1)`,
				app.ID).Scan(&apps, &ops); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 1, false: 0}[tt.liveClaim]; apps != want || ops != want {
				t.Fatalf("after purge apps=%d operations=%d, want %d each", apps, ops, want)
			}
		})
	}
}

func expireApp(t *testing.T, ctx context.Context, st *state.PgStore, appID string) {
	t.Helper()
	if _, err := st.ScheduleAppDeletion(ctx, appID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimAppDeletion(ctx, appID); err != nil {
		t.Fatal(err)
	}
}
