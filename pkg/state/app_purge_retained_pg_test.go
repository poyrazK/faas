//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 11 H11-21: on production-us six deleted apps failed their purge every
// minute for days on RESTRICT keys from finished rows: deleted managed
// PostgreSQL binding tombstones and settled multipart part writers. The purge
// now removes finished rows and waits (ErrConflict) for an uncertain
// (dispatched, unsettled) part write, which ADR-590 retains.
func TestPgDeleteAppPermanentlyRetainedResources(t *testing.T) {
	tests := []struct {
		name    string
		writer  string // "", "reserved", "dispatched", "settled"
		binding string // "", "deleted"
		wantErr error
	}{
		{"settled writer and deleted binding are purged", "settled", "deleted", nil},
		{"never-dispatched writer is purged", "reserved", "", nil},
		{"uncertain part write waits", "dispatched", "", state.ErrConflict},
		// A live binding cannot reach the purge: deleting the app is refused
		// while it has one ("app has managed postgres bindings").
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, pool, ctx := pgStoreWithPool(t)
			b, u := activeTrackedUpload(t, st, "h12-part")
			if tt.writer != "" {
				if err := st.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "attempt", 1, 30, 100, accountingPolicy()); err != nil {
					t.Fatal(err)
				}
			}
			if tt.writer == "dispatched" || tt.writer == "settled" {
				r, err := st.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "attempt")
				if err != nil {
					t.Fatal(err)
				}
				if tt.writer == "settled" {
					if err := st.FinishObjectMultipartPartMutation(ctx, r); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tt.binding != "" {
				database := uuid.NewString()
				if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,'h12db','eu',17,'development','single_zone','test',$3)`,
					database, b.AccountID, strings.Repeat("a", 64)); err != nil {
					t.Fatal(err)
				}
				binding := uuid.NewString()
				if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,'default','DATABASE_URL','read_write')`,
					binding, b.AccountID, database, b.AppID); err != nil {
					t.Fatal(err)
				}
				if tt.binding == "deleted" {
					if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state='deleted', deleted_at=now() WHERE id=$1`, binding); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := pool.Exec(ctx, `UPDATE object_buckets SET state='deleted' WHERE id=$1`, b.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.ScheduleAppDeletion(ctx, b.AppID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := st.ClaimAppDeletion(ctx, b.AppID); err != nil {
				t.Fatal(err)
			}
			err := st.DeleteAppPermanently(ctx, b.AppID)
			if (tt.wantErr == nil && err != nil) || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Fatalf("DeleteAppPermanently = %v, want %v", err, tt.wantErr)
			}
			var apps int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM apps WHERE id=$1`, b.AppID).Scan(&apps); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 0, false: 1}[tt.wantErr == nil]; apps != want {
				t.Fatalf("apps rows after purge = %d, want %d", apps, want)
			}
		})
	}
}
