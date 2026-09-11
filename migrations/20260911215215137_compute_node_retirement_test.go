//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigration_ComputeNodeRetirement(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	var retired string
	if err := pool.QueryRow(t.Context(), `select 'retired'::compute_node_lifecycle::text`).Scan(&retired); err != nil {
		t.Fatalf("retired lifecycle enum: %v", err)
	}
	if body := mustQueryKindCheckConsrc(t, pool); !strings.Contains(body, "'node_retire'") {
		t.Fatalf("operator_intents_kind_check missing node_retire: %s", body)
	}
}
