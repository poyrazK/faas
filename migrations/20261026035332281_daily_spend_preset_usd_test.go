//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// Hunt 14 H14-69: the billing-currency switch to USD retitled spend_eur_20
// but left daily_spend_eur_1 saying "€1". After the migration both spend
// presets name dollars; the preset keeps the name customers enable it by.
func TestDailySpendPresetUSDMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var title, description string
	if err := pool.QueryRow(ctx, `SELECT display_name, description FROM alert_presets WHERE name = 'daily_spend_eur_1'`).Scan(&title, &description); err != nil {
		t.Fatal(err)
	}
	if title != "Daily usage cost exceeds $1" || strings.Contains(description, "€") || !strings.Contains(description, "$1") {
		t.Fatalf("daily_spend_eur_1 = %q / %q, want dollars", title, description)
	}
}
