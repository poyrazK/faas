//go:build !no_pg

package state_test

import "testing"

func TestRetryPreservesEnvironmentPinPG(t *testing.T) {
	store, ctx := pgStore(t)
	testRetryPreservesEnvironmentPin(t, store, ctx)
}

func TestRetryPreservesEnvironmentLifetimePG(t *testing.T) {
	store, ctx := pgStore(t)
	testRetryPreservesEnvironmentLifetime(t, store, ctx)
}

func TestRetryPreservesStartupCPUBoostPG(t *testing.T) {
	store, ctx := pgStore(t)
	testRetryPreservesStartupCPUBoost(t, store, ctx)
}
