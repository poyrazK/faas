//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigration_OperatorDatabaseRestartIntent(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)
	if body := mustQueryKindCheckConsrc(t, pool); !strings.Contains(body, "'database_restart'") || !strings.Contains(body, "'database_switchover'") || !strings.Contains(body, "'database_move'") {
		t.Fatalf("operator_intents_kind_check lacks database_restart, database_switchover or an earlier kind: %s", body)
	}
}
