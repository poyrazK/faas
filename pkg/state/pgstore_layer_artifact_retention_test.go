//go:build !no_pg

package state_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
)

// ADR-583: exercise real transaction/trigger fencing alongside MemStore.
func TestPgCloneLayerRetentionSurvivesSourceRetirement(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneLayerRetentionSurvivesSourceRetirement(t, s)
}

func TestPgLayerDeletionFencesConcurrentPublication(t *testing.T) {
	s, _, _ := pgWithPool(t)
	layerDeletionFencesConcurrentPublication(t, s)
}

// ADR-583: the persistent reference guards also cover snapshot/VM writers.
func TestPgLayerRetentionConsumerReferences(t *testing.T) {
	s, _, _ := pgWithPool(t)
	layerRetentionConsumerReferences(t, s)
}

// ADR-583: an upgrade pins private workload captures made before this catalogue.
func TestPgCloneLayerRetentionBackfillsExistingCaptures(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	cloneLayerRetentionSurvivesSourceRetirement(t, s)
	raw, err := migrations.FS.ReadFile("20261004095153116_clone_layer_artifact_retention.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, found := strings.Cut(string(raw), "-- +goose Down")
	if !found {
		t.Fatal("migration has no down section")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	var count, bytes int64
	if err := pool.QueryRow(ctx, `select count(*), coalesce(sum(bytes), 0) from project_environment_clone_layer_pins`).Scan(&count, &bytes); err != nil {
		t.Fatal(err)
	}
	if count != 2 || bytes != 4608 {
		t.Fatalf("backfilled private rootfs/sidecar pins: count=%d, bytes=%d", count, bytes)
	}
}
