//go:build !no_pg

package state_test

import "testing"

func TestDeploymentRetryPG(t *testing.T) { store, _ := pgStore(t); testDeploymentRetry(t, store) }
