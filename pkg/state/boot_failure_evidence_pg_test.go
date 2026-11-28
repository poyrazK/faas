package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-605 amendment 2: the PostgreSQL boot evidence counts only this
// deployment's live failed VM boots and completed boots inside the window,
// and agrees with the in-memory reader on the same events.
func TestDeploymentBootEvidencePG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	app, dep, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, ev := range []struct {
		kind string
		data map[string]any
		ago  time.Duration
	}{
		{"wake.boot_failed", map[string]any{"app_id": app, "deployment_id": dep, "reason": "vmm_boot_failed"}, time.Minute},
		{"wake.boot_failed", map[string]any{"app_id": app, "deployment_id": dep, "reason": "vmm_boot_failed"}, 2 * time.Minute},
		{"wake.boot_failed", map[string]any{"app_id": app, "deployment_id": dep, "reason": "vmm_boot_failed", "mirror": true}, time.Minute},
		{"wake.boot_failed", map[string]any{"app_id": app, "deployment_id": dep, "reason": "record_runtime_failed"}, time.Minute},
		{"wake.boot_failed", map[string]any{"app_id": app, "deployment_id": other, "reason": "vmm_boot_failed"}, time.Minute},
		{"wake.boot_failed", map[string]any{"app_id": app, "deployment_id": dep, "reason": "vmm_boot_failed"}, time.Hour},
		{"wake.boot_failed", map[string]any{"app_id": app, "reason": "vmm_boot_failed"}, time.Minute},
		{"wake.boot_completed", map[string]any{"app_id": app, "deployment_id": other}, time.Minute},
	} {
		data, _ := json.Marshal(ev.data)
		if err := s.AppendEventAt(ctx, "schedd", ev.kind, nil, data, now.Add(-ev.ago)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(deployment string) (int64, int64) {
		t.Helper()
		row, err := sqlc.New().ReadDeploymentBootEvidence(ctx, pool, sqlc.ReadDeploymentBootEvidenceParams{
			AppID: app, DeploymentID: deployment,
			WindowStart: pgtype.Timestamptz{Time: now.Add(-15 * time.Minute), Valid: true},
			WindowEnd:   pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		return row.Failures, row.Successes
	}
	if failures, successes := read(dep); failures != 2 || successes != 0 {
		t.Fatalf("evidence = %d failures, %d successes; want 2, 0", failures, successes)
	}
	if failures, successes := read(other); failures != 1 || successes != 1 {
		t.Fatalf("other deployment evidence = %d, %d; want 1, 1", failures, successes)
	}
}
