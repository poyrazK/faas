//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestPromotionControlPG(t *testing.T) {
	testPromotionControlStore(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestPromotionControlWorkerPG(t *testing.T) {
	testPromotionControlWorker(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}
