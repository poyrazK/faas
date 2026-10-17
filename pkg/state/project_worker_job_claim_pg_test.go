//go:build !no_pg

package state_test

import "testing"

func TestProjectWorkerJobClaimPG(t *testing.T) { store, _ := pgStore(t); testWorkerJobClaim(t, store) }
