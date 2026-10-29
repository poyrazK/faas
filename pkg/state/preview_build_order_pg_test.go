//go:build !no_pg

package state_test

import "testing"

func TestPgStoreClaimsProductionBuildsBeforePreviewBuilds(t *testing.T) {
	s, ctx := pgStore(t)
	previewBuildOrderContract(t, ctx, s)
}
