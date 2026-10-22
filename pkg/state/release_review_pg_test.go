//go:build !no_pg

package state_test

import "testing"

func TestPgReleaseReview(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testReleaseReview(t, store)
}

func TestPgReviewedNamedStaging(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testReviewedNamedStaging(t, store)
}

func TestPgReviewedRollout(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testReviewedRollout(t, store)
}

func TestPgReviewedServiceRecovery(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testReviewedServiceRecovery(t, store)
}

func TestPgReviewedPinnedServiceReadiness(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testReviewedPinnedServiceReadiness(t, store, resolveDefaultLocal(t, ctx, store))
}
