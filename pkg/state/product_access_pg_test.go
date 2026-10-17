//go:build !no_pg

// ADR-1542: PostgreSQL policy persistence uses the same catalog contract as Memory.
package state

import "testing"

func TestProductControlAccessPostgres(t *testing.T) { productAccessContract(t, newPgStore(t)) }
