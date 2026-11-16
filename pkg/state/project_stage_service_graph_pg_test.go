//go:build !no_pg

package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestStageServiceGraphPG(t *testing.T) {
	testStageServiceGraphBeforeReleaseSets(t, NewPgStore(pgtest.OpenMigrated(t)))
}
