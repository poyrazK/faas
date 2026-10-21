//go:build !no_pg

package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestProjectServiceAdditionsPG(t *testing.T) {
	testProjectServiceAdditions(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestProjectServiceAdditionQuotaPG(t *testing.T) {
	testProjectServiceAdditionQuota(t, NewPgStore(pgtest.OpenMigrated(t)))
}
