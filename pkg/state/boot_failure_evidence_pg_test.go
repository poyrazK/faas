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
// deployment's live failed VM boots inside the window, and only on nodes
// that completed a boot of another deployment in the window, so a node or
// fleet fault never reads as the release failing.
func TestDeploymentBootEvidencePG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	app, dep, other, own, neighbour := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	healthy, broken, lone := "node-"+uuid.NewString(), "node-"+uuid.NewString(), "node-"+uuid.NewString()
	failed := func(node, deployment string, extra map[string]any) map[string]any {
		d := map[string]any{"app_id": app, "deployment_id": deployment, "node_id": node, "reason": "vmm_boot_failed"}
		for k, v := range extra {
			d[k] = v
		}
		return d
	}
	completed := func(node, appID, deployment string) map[string]any {
		return map[string]any{"app_id": appID, "deployment_id": deployment, "node_id": node}
	}
	for _, ev := range []struct {
		kind string
		data map[string]any
		ago  time.Duration
	}{
		{"wake.boot_failed", failed(healthy, dep, nil), time.Minute},
		{"wake.boot_failed", failed(healthy, dep, nil), 2 * time.Minute},
		{"wake.boot_failed", failed(healthy, dep, map[string]any{"mirror": true}), time.Minute},
		{"wake.boot_failed", failed(healthy, dep, map[string]any{"reason": "record_runtime_failed"}), time.Minute},
		{"wake.boot_failed", failed(healthy, dep, nil), time.Hour},
		{"wake.boot_failed", map[string]any{"app_id": app, "node_id": healthy, "reason": "vmm_boot_failed"}, time.Minute},
		// A node that started nothing else in the window is not known
		// healthy: failures there count against no release.
		{"wake.boot_failed", failed(broken, dep, nil), time.Minute},
		{"wake.boot_failed", failed(broken, other, nil), time.Minute},
		{"wake.boot_completed", completed(healthy, uuid.NewString(), neighbour), 3 * time.Minute},
		{"wake.boot_completed", completed(healthy, app, other), time.Minute},
		// A release's own completed boot does not vouch for the node.
		{"wake.boot_failed", failed(lone, own, nil), time.Minute},
		{"wake.boot_completed", completed(lone, app, own), 2 * time.Minute},
		// Completions outside the window vouch for nothing.
		{"wake.boot_completed", completed(broken, uuid.NewString(), neighbour), time.Hour},
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
	for _, want := range []struct {
		deployment          string
		failures, successes int64
	}{{dep, 2, 0}, {other, 0, 1}, {own, 0, 1}} {
		if failures, successes := read(want.deployment); failures != want.failures || successes != want.successes {
			t.Errorf("deployment %s evidence = %d failures, %d successes; want %d, %d", want.deployment, failures, successes, want.failures, want.successes)
		}
	}
}
