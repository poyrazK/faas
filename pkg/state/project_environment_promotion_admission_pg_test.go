//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestPromotionAdmissionPG(t *testing.T) {
	testPromotionAdmission(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}
func TestPromotionAdmissionConcurrentPG(t *testing.T) {
	testConcurrentAdmission(t, NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPromotionAdmissionFleetQueuePG(t *testing.T) {
	testFleetQueueAdmission(t, NewPgStore(pgtest.OpenMigrated(t)))
}
func TestPromotionAdmissionDispatchConcurrentPG(t *testing.T) {
	testConcurrentAdmissionDispatch(t, NewPgStore(pgtest.OpenMigrated(t)))
}
