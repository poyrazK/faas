//go:build !no_pg

// ADR-1708: PostgreSQL policy persistence uses the same catalog contract as Memory.
package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlAccessPostgres(t *testing.T) { productAccessContract(t, state.XNewPgStore(t)) }
