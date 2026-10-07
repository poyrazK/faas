//go:build !no_pg

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOwnedBucketCleanupFencePG(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	ownedBucketCleanupSuite(t, e.s, e.store, e.acct)
}
