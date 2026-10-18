//go:build !no_pg

package state

import "testing"

func TestProductControlSetupPostgres(t *testing.T) { productSetupContract(t, newPgStore(t)) }
