//go:build !no_pg

package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPromotionServiceGraphPG(t *testing.T) {
	testPromotionServiceGraph(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}
