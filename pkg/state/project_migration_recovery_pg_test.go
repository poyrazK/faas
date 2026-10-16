//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestMigrationRecoveryPG(t *testing.T) {
	testMigrationRecovery(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}
