//go:build !no_pg

// ADR-1715: PostgreSQL and memory share help authority, retry and concurrency contracts.
package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlHelpPostgres(t *testing.T) { state.ProductHelpContract(t, state.XNewPgStore(t)) }
