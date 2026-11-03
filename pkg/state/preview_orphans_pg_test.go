//go:build !no_pg

package state_test

import "testing"

func TestPgStoreMarksOrphanedPRPreviews(t *testing.T) {
	s, ctx := pgStore(t)
	orphanedPRPreviewContract(t, ctx, s)
}
