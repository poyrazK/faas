//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigration_OperatorNodeIntentKinds(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	body := mustQueryKindCheckConsrc(t, pool)
	for _, want := range []string{
		"'force_park'",
		"'force_cold_boot'",
		"'force_restart'",
		"'node_drain'",
		"'node_force_drain'",
		"'node_activate'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("operator_intents_kind_check body missing %s; full body=%s", want, body)
		}
	}
}
