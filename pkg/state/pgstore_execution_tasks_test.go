//go:build !no_pg

package state_test

import "testing"

func TestPgVersionedRunTasks(t *testing.T) { s, _, _ := pgStoreWithPool(t); exerciseRunTasks(t, s) }
