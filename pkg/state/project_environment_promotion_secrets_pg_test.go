//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestPromotionSecretsPG(t *testing.T) {
	testPromotionSecrets(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestPromotionSecretRetirementPG(t *testing.T) {
	testPromotionSecretRetirement(t, NewPgStore(pgtest.OpenMigrated(t)))
}
