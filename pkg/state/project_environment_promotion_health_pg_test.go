//go:build !no_pg

package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPromotionHealthPG(t *testing.T) {
	testPromotionHealthStore(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}
