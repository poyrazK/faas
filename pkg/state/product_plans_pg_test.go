//go:build !no_pg

package state

import "testing"

func TestProductControlSelfServicePostgres(t *testing.T) { productPlansContract(t, newPgStore(t)) }
