//go:build !no_pg

package sched

// adr: 595 Same serving restore lifecycle with durable PostgreSQL transactions.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresApplicationStandardServingRestore(t *testing.T) {
	for _, variant := range []string{"serving restore", "unsupported cold fallback", "load cold fallback", "substituted proof"} {
		t.Run(variant, func(t *testing.T) {
			exerciseStandardServingRestore(t, state.NewPgStore(pgtest.OpenMigrated(t)), variant)
		})
	}
}
