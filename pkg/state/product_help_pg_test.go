//go:build !no_pg

// ADR-1715: PostgreSQL and memory share help authority, retry and concurrency contracts.
package state

import "testing"

func TestProductControlHelpPostgres(t *testing.T) { productHelpContract(t, newPgStore(t)) }
