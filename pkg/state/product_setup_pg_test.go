//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlSetupPostgres(t *testing.T) { productSetupContract(t, state.XNewPgStore(t)) }
