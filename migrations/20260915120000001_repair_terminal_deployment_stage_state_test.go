//go:build !no_pg

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

const repairTerminalStageStateVersion int64 = 20260915120000001

func TestMigrations_RepairTerminalDeploymentStageState(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	accountID := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, accountID)
	started := time.Now().UTC().Add(-time.Minute)

	tests := []struct {
		status      string
		wantHistory string
		wantCount   int
	}{
		{status: "failed", wantHistory: "failed", wantCount: state.MaxStageHistory},
		{status: "superseded", wantHistory: "cancelled", wantCount: 1},
		{status: "live", wantHistory: "completed", wantCount: 1},
	}
	ids := make([]string, 0, len(tests))
	for _, tc := range tests {
		id := seedDeployment(t, ctx, pool, appID, tc.status)
		ids = append(ids, id)
		if _, err := pool.Exec(ctx, `
			update deployments
			set stage_state = jsonb_build_object(
				'current', 'image_build',
				'current_started_at', $2::timestamptz,
				'history', '[]'::jsonb
			), error_code = case when status = 'failed' then 'app_layer_too_large' else error_code end
			where id = $1`, id, started); err != nil {
			t.Fatalf("seed stale %s stage: %v", tc.status, err)
		}
		if tc.status == "failed" {
			if _, err := pool.Exec(ctx, `
				update deployments
				set stage_state = jsonb_set(
					stage_state,
					'{history}',
					(select jsonb_agg(jsonb_build_object(
						'name', 'source_download',
						'status', 'completed',
						'duration_ms', n
					) order by n) from generate_series(1, 64) as n)
				)
				where id = $1`, id); err != nil {
				t.Fatalf("seed capped stage history: %v", err)
			}
		}
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = $1`, repairTerminalStageStateVersion); err != nil {
		t.Fatalf("rewind repair migration: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply repair migration: %v", err)
	}

	for i, tc := range tests {
		var raw []byte
		if err := pool.QueryRow(ctx, `select stage_state from deployments where id = $1`, ids[i]).Scan(&raw); err != nil {
			t.Fatalf("read repaired %s row: %v", tc.status, err)
		}
		var got state.StageState
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode repaired %s row: %v", tc.status, err)
		}
		if got.Current != "" || got.CurrentStartedAt != nil {
			t.Errorf("%s current stage was not cleared: %+v", tc.status, got)
		}
		if len(got.History) != tc.wantCount {
			t.Errorf("%s history count = %d, want %d", tc.status, len(got.History), tc.wantCount)
		} else if got.History[len(got.History)-1].Status != tc.wantHistory {
			t.Errorf("%s final history status = %q, want %q", tc.status, got.History[len(got.History)-1].Status, tc.wantHistory)
		}
	}
}
