//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestProjectReleaseSecretsPG(t *testing.T) {
	testProjectReleaseSecrets(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}
