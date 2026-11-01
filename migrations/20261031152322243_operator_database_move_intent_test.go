//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigration_OperatorDatabaseMoveIntent(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)
	if body := mustQueryKindCheckConsrc(t, pool); !strings.Contains(body, "'database_move'") || !strings.Contains(body, "'node_retire'") {
		t.Fatalf("operator_intents_kind_check lacks database_move or an earlier kind: %s", body)
	}
}
