//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlSelfServicePostgres(t *testing.T) {
	state.ProductPlansContract(t, state.XNewPgStore(t))
}
