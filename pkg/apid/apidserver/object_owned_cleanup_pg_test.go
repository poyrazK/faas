//go:build !no_pg

package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestOwnedBucketCleanupFencePG(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanHobby)
	apidserver.OwnedBucketCleanupSuite(t, (*e.S()), (*e.Store()), (*e.Acct()))
}
